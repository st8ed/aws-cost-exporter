package main

import (
	"context"
	"net/http"
	"os"
	"os/user"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"golang.org/x/sync/errgroup"
	kingpin "gopkg.in/alecthomas/kingpin.v2"

	"github.com/st8ed/aws-cost-exporter/pkg/collector"
	"github.com/st8ed/aws-cost-exporter/pkg/fetcher"
	"github.com/st8ed/aws-cost-exporter/pkg/processor"
	"github.com/st8ed/aws-cost-exporter/pkg/state"
)

type intervalGatherer struct {
	gatherer prometheus.Gatherer
	cache    atomic.Pointer[[]*dto.MetricFamily]
}

// Gather implements prometheus.Gatherer.
func (ig *intervalGatherer) Gather() ([]*dto.MetricFamily, error) {
	mfs := ig.cache.Load()
	if mfs == nil {
		return nil, nil
	}
	return *mfs, nil
}

// Run will gather metrics from the gatherer at the given interval until the context is done.
func (ig *intervalGatherer) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		mfs, err := ig.gatherer.Gather()
		if err != nil {
			return err
		}
		ig.cache.Store(&mfs)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func newGatherer(ctx context.Context, interval time.Duration, config *state.Config, state *state.State, client *s3.Client, logger log.Logger) (prometheus.Gatherer, error) {
	reg := prometheus.NewRegistry()

	periods, err := fetcher.GetBillingPeriods(config, client)
	if err != nil {
		return nil, err
	}

	state.Periods = periods

	if err := collector.Prefetch(state, config, client, reg, periods, logger); err != nil {
		return nil, err
	}

	if err := state.Save(config); err != nil {
		return nil, err
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, interval)
	defer cancel()
	if err := processor.Compute(ctxWithTimeout, config, reg, logger); err != nil {
		return nil, err
	}

	return prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, interval)
		defer cancel()
		if len(state.Periods) > 0 {
			period := state.Periods[len(state.Periods)-1]

			if period.IsPastDue() {
				periods, err := fetcher.GetBillingPeriods(config, client)
				if err != nil {
					return nil, err
				}

				state.Periods = periods
				period = periods[len(periods)-1]
			}

			changed, err := collector.UpdateReport(state, config, client, &period, logger)
			if err != nil {
				return nil, err
			}

			if changed {
				if err := state.Save(config); err != nil {
					return nil, err
				}

				if err := processor.Compute(ctxWithTimeout, config, reg, logger); err != nil {
					return nil, err
				}
			}
		}

		return reg.Gather()
	}), nil
}

func main() {
	var (
		bucketName = kingpin.Flag(
			"bucket",
			"Name of the S3 bucket with detailed billing report(s)",
		).Required().String()

		interval = kingpin.Flag(
			"interval",
			"How long to wait between background computations of the billing report.",
		).Default("5m").Duration()

		reportName = kingpin.Flag(
			"report",
			"Name of the AWS detailed billing report in supplied S3 bucket",
		).Required().String()

		repositoryPath = kingpin.Flag(
			"repository",
			"Path to store cached AWS billing reports",
		).Default("/var/lib/aws-cost-exporter/repository").String()

		queriesPath = kingpin.Flag(
			"queries-dir",
			"Path to directory with SQL queries for gathering metrics",
		).Default("/etc/aws-cost-exporter/queries").String()

		stateFilePath = kingpin.Flag(
			"state-path",
			"Path to store exporter state",
		).Default("/var/lib/aws-cost-exporter/state.json").String()

		listenAddress = kingpin.Flag(
			"web.listen-address",
			"Address on which to expose metrics and web interface.",
		).Default(":9100").String()

		metricsPath = kingpin.Flag(
			"web.telemetry-path",
			"Path under which to expose metrics.",
		).Default("/metrics").String()

		disableExporterMetrics = kingpin.Flag(
			"web.disable-exporter-metrics",
			"Exclude metrics about the exporter itself (promhttp_*, process_*, go_*).",
		).Bool()

		configFile = kingpin.Flag(
			"web.config",
			"[EXPERIMENTAL] Path to config yaml file that can enable TLS or authentication.",
		).Default("").String()
	)

	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("aws-cost-exporter"))
	kingpin.CommandLine.UsageWriter(os.Stdout)
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	logger := promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting aws-cost-exporter", "version", version.Info())
	level.Info(logger).Log("msg", "Build context", "build_context", version.BuildContext())
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		level.Warn(logger).Log("msg", "AWS Cost Exporter is running as root user. This exporter is designed to run as unpriviledged user, root is not required.")
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithDefaultRegion("us-east-1"))
	if err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}

	client := s3.NewFromConfig(cfg)

	config := &state.Config{
		RepositoryPath: *repositoryPath,
		QueriesPath:    *queriesPath,
		StateFilePath:  *stateFilePath,

		BucketName: *bucketName,
		ReportName: *reportName,
	}

	state, err := state.Load(config)
	if err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}

	g, ctx := errgroup.WithContext(context.Background())

	gatherer, err := newGatherer(ctx, *interval, config, state, client, logger)
	if err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}

	gatherer = &intervalGatherer{
		gatherer: gatherer,
	}
	gatherers := prometheus.Gatherers{gatherer}
	if !*disableExporterMetrics {
		reg := prometheus.NewRegistry()
		reg.MustRegister(collectors.NewBuildInfoCollector())
		reg.MustRegister(collectors.NewGoCollector(
			collectors.WithGoCollections(collectors.GoRuntimeMemStatsCollection | collectors.GoRuntimeMetricsCollection),
		))
		gatherers = append(gatherers, reg)
	}

	g.Go(func() error {
		return gatherer.(*intervalGatherer).Run(ctx, *interval)
	})

	http.Handle(*metricsPath, promhttp.HandlerFor(
		gatherers,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>AWS Cost Exporter</title></head>
			<body>
			<h1>AWS Cost Exporter</h1>
			<p><a href="` + *metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	level.Info(logger).Log("msg", "Listening on", "address", *listenAddress)
	server := &http.Server{Addr: *listenAddress}
	g.Go(func() error {
		return web.ListenAndServe(server, *configFile, logger)
	})

	go func() {
		// If the group's context is cancelled, then one of the
		// concurrent goroutines returned an error or both finished.
		// Clean up all of the goroutines to make sure that `Wait`
		// will return and the program can exit. The intervalGatherer
		// will already be stopped by virtue of the context being done
		// so we just need to ensure that the server is shutdown.
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	if err := g.Wait(); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}
