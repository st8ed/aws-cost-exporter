package processor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/st8ed/aws-cost-exporter/pkg/state"

	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"time"

	_ "github.com/mithrandie/csvq-driver"
)

func Compute(config *state.Config, registry *prometheus.Registry, logger log.Logger) error {
	if err := updateSymlinks(config); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sql.Open("csvq", config.RepositoryPath)
	if err != nil {
		return err
	} else {
		level.Debug(logger).Log("msg", "Opened database", "repository", config.RepositoryPath)
	}
	defer func() {
		if err := db.Close(); err != nil {
			level.Warn(logger).Log("msg", "Unable to close database", "err", err)
		}
		level.Debug(logger).Log("msg", "Closed database")
	}()

	items, err := os.ReadDir(config.QueriesPath)
	if err != nil {
		return err
	}

	for _, item := range items {
		if item.IsDir() {
			continue
		}

		// TODO: Basic file validation (extension)
		query, err := os.ReadFile(filepath.Join(config.QueriesPath, item.Name()))
		if err != nil {
			return err
		}

		level.Debug(logger).Log("msg", "Running query", "name", item.Name())

		rows, err := db.QueryContext(ctx, string(query))
		if err != nil {
			return err
		}

		level.Debug(logger).Log("msg", "Updating metrics registry")
		ingestMetrics(registry, rows)
	}

	return nil
}

func ingestMetrics(registry *prometheus.Registry, rows *sql.Rows) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	if len(columns) == 0 {
		return errors.New("Malformed query: there are no columns in result set")
	}

	labelNames := make([]string, 0)
	labelValuePtrs := make([]*string, 0)

	metricNames := make([]string, 0)
	metricValuePtrs := make([]*float64, 0)

	rowValuePtrs := make([]interface{}, len(columns))

	for i, column := range columns {
		if strings.HasPrefix(column, "metric_") {
			// FIXME: Check metric name using regexp
			value := new(float64)

			metricNames = append(metricNames, strings.TrimPrefix(column, "metric_"))
			metricValuePtrs = append(metricValuePtrs, value)
			rowValuePtrs[i] = value
		} else {
			value := new(string)

			labelNames = append(labelNames, column)
			labelValuePtrs = append(labelValuePtrs, value)
			rowValuePtrs[i] = value
		}
	}

	gauges := make([]*prometheus.GaugeVec, len(metricNames))

	for i, name := range metricNames {
		gauges[i] = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("aws_report_%s", name),
			},
			labelNames,
		)
		if err := registry.Register(gauges[i]); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				gauges[i] = are.ExistingCollector.(*prometheus.GaugeVec)
			} else {
				return err
			}
		}

	}

	for rows.Next() {
		if err := rows.Scan(rowValuePtrs...); err != nil {
			if err == sql.ErrNoRows {
				break
			} else {
				return err
			}
		} else {
			labelValues := make([]string, len(labelValuePtrs))

			for i := range labelValuePtrs {
				labelValues[i] = *labelValuePtrs[i]
			}

			for i, gauge := range gauges {
				gauge.WithLabelValues(labelValues...).Set(*metricValuePtrs[i])
			}
		}
	}

	return nil
}

func updateSymlinks(config *state.Config) error {
	items, err := os.ReadDir(filepath.Join(config.RepositoryPath, "data"))
	if err != nil {
		return err
	}

	names := make([]string, 0)

	for _, item := range items {
		if item.IsDir() {
			continue
		}
		names = append(names, item.Name())
	}

	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	for i, name := range names {
		var source string

		if i == 0 {
			source = "report-current.csv"
		} else {
			source = fmt.Sprintf("report-%d.csv", i)
		}

		source = filepath.Join(config.RepositoryPath, source)
		target := filepath.Join(".", "data", name)

		if err := symlink(source, target); err != nil {
			return err
		}
	}

	return nil
}

// Atomically overwrite symbolic link
func symlink(source string, target string) error {
	sourceTmp := source + ".tmp"

	if err := os.Remove(sourceTmp); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.Symlink(target, sourceTmp); err != nil {
		return err
	}

	if err := os.Rename(sourceTmp, source); err != nil {
		return err
	}

	return nil
}
