package fetcher

import (
	"github.com/st8ed/aws-cost-exporter/pkg/state"

	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type ReportManifest struct {
	AssemblyId    string `json:"assemblyId"`
	Compression   string `json:"compression"`
	ContentType   string `json:"contentType"`
	BillingPeriod struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"billingPeriod"`
	Bucket     string   `json:"bucket"`
	ReportKeys []string `json:"reportKeys"`
}

type SortRecentFirst []state.BillingPeriod

func (a SortRecentFirst) Len() int           { return len(a) }
func (a SortRecentFirst) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a SortRecentFirst) Less(i, j int) bool { return a[i] < a[j] }

func GetBillingPeriods(config *state.Config, client *s3.Client) ([]state.BillingPeriod, error) {
	params := &s3.ListObjectsV2Input{
		Bucket:    aws.String(config.BucketName),
		Prefix:    aws.String("/" + config.ReportName + "/"),
		Delimiter: aws.String("/"),
	}

	periods := make([]state.BillingPeriod, 0)
	p := s3.NewListObjectsV2Paginator(client, params)

	for p.HasMorePages() {
		page, err := p.NextPage(context.TODO())
		if err != nil {
			return nil, err
		}

		for _, obj := range page.CommonPrefixes {
			period, err := state.ParseBillingPeriod(
				strings.TrimSuffix(strings.TrimPrefix(*obj.Prefix, *params.Prefix), "/"),
			)

			if err != nil {
				return nil, err
			}

			periods = append(periods, *period)
		}
	}

	sort.Sort(SortRecentFirst(periods))

	if len(periods) > 3 {
		return periods[len(periods)-3:], nil
	} else {
		return periods, nil
	}
}

func GetReportManifest(config *state.Config, client *s3.Client, period *state.BillingPeriod, lastModified *time.Time) (*ReportManifest, error) {
	params := &s3.GetObjectInput{
		Bucket: aws.String(config.BucketName),
		Key: aws.String(fmt.Sprintf(
			"/%s/%s/%s-Manifest.json",
			config.ReportName, string(*period), config.ReportName,
		)),
		IfModifiedSince: aws.Time(*lastModified),
	}

	obj, err := client.GetObject(context.TODO(), params)
	if err != nil {
		var ae smithy.APIError

		if !errors.As(err, &ae) {
			return nil, err
		}

		if ae.ErrorCode() == "NotModified" {
			return nil, nil
		} else {
			return nil, err
		}
	}
	defer obj.Body.Close()

	*lastModified = *obj.LastModified
	manifest := &ReportManifest{}

	decoder := json.NewDecoder(obj.Body)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}

	if manifest.ContentType != "text/csv" {
		return nil, fmt.Errorf("report manifest contains unknown content type: %s", manifest.ContentType)
	}

	if manifest.Bucket != config.BucketName {
		return nil, fmt.Errorf("report manifest contains unexpected bucket name: %s", manifest.Bucket)
	}

	if len(manifest.ReportKeys) == 0 {
		return nil, fmt.Errorf("report manifest contains no report keys")
	}

	return manifest, nil
}

func FetchReport(config *state.Config, client *s3.Client, manifest *ReportManifest, logger log.Logger) error {
	periodStart, err := time.Parse("20060102T150405Z", manifest.BillingPeriod.Start)
	if err != nil {
		return err
	}

	reportFile := filepath.Join(
		config.RepositoryPath, "data",
		periodStart.Format("20060102")+"-"+manifest.AssemblyId+".csv",
	)

	if _, err := os.Stat(reportFile); !errors.Is(err, os.ErrNotExist) {
		level.Warn(logger).Log("msg", "Report file already exists, skipping download", "file", reportFile)
		return nil
	}

	level.Info(logger).Log("msg", "Fetching report", "file", reportFile, "parts", len(manifest.ReportKeys))

	f, err := os.Create(reportFile + ".tmp")
	if err != nil {
		return err
	}

	for reportPart, reportKey := range manifest.ReportKeys {
		level.Info(logger).Log("msg", "Fetching report part", "file", reportFile, "part", reportPart)

		params := &s3.GetObjectInput{
			Bucket: aws.String(manifest.Bucket),
			Key:    aws.String(reportKey),
		}

		piper, pipew := io.Pipe()

		writeErr := make(chan error)

		go func() {
			defer piper.Close()

			zr, err := gzip.NewReader(piper)
			if err != nil {
				writeErr <- err
				return
			}
			defer zr.Close()

			if reportPart > 0 {
				// Keep table header (first csv line)
				// only for first report partition
				headerReader := bufio.NewReaderSize(zr, 1)

				if _, err := headerReader.ReadString('\n'); err != nil {
					writeErr <- err
					return
				}
			}

			if _, err := io.Copy(f, zr); err != nil {
				writeErr <- err
				return
			}

			writeErr <- nil
		}()

		obj, err := client.GetObject(context.TODO(), params)
		if err != nil {
			return err
		}
		defer obj.Body.Close()

		level.Debug(logger).Log("ContentLength", obj.ContentLength)

		if written, err := io.Copy(pipew, obj.Body); err != nil {
			return err
		} else {
			level.Debug(logger).Log("Written", written)
		}

		pipew.Close()

		if err := <-writeErr; err != nil {
			return err
		}
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(reportFile+".tmp", reportFile); err != nil {
		return err
	}

	return nil
}
