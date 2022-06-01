package main

import (
	"github.com/st8ed/aws-cost-exporter/pkg/collector"
	"github.com/st8ed/aws-cost-exporter/pkg/fetcher"
	"github.com/st8ed/aws-cost-exporter/pkg/processor"
	"github.com/st8ed/aws-cost-exporter/pkg/state"

	"context"
	"os/user"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/version"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"

	"net/http"
	"os"

	"github.com/prometheus/exporter-toolkit/web"

	_ "github.com/mithrandie/csvq-driver"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

func newGatherer(config *state.Config, state *state.State, client *s3.Client, disableExporterMetrics bool, logger log.Logger) (prometheus.GathererFunc, error) {
	reg := prometheus.NewRegistry()

	if !disableExporterMetrics {
		reg.MustRegister(collectors.NewBuildInfoCollector())
		reg.MustRegister(collectors.NewGoCollector(
			collectors.WithGoCollections(collectors.GoRuntimeMemStatsCollection | collectors.GoRuntimeMetricsCollection),
		))
	}

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

	if err := processor.Compute(config, reg, logger); err != nil {
		return nil, err
	}

	return prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
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

				if err := processor.Compute(config, reg, logger); err != nil {
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

		reportName = kingpin.Flag(
			"report",
			"Name of the AWS detailed billing report in supplied S3 bucket",
		).Required().String()

		repositoryPath = kingpin.Flag(
			"repository",
			"Path to store cached AWS billing reports",
		).Default("/var/lib/aws-billing-reports/repository").String()

		queriesPath = kingpin.Flag(
			"queries-dir",
			"Path to directory with SQL queries for gathering metrics",
		).Default("/var/lib/aws-billing-reports/queries").String()

		stateFilePath = kingpin.Flag(
			"state-path",
			"Path to store exporter state",
		).Default("/var/lib/aws-billing-reports/state.json").String()

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

	gatherer, err := newGatherer(config, state, client, *disableExporterMetrics, logger)
	if err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}

	http.Handle(*metricsPath, promhttp.HandlerFor(
		gatherer,
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
	if err := web.ListenAndServe(server, *configFile, logger); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}
