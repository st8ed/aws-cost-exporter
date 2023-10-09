# AWS Cost Exporter

[![Build Status](https://github.com/st8ed/aws-cost-exporter/actions/workflows/build-publish.yml/badge.svg)](https://github.com/st8ed/aws-cost-exporter/actions)
[![Go Report Card](https://goreportcard.com/badge/st8ed/aws-cost-exporter "Go Report Card")](https://goreportcard.com/report/st8ed/aws-cost-exporter)

> An easy to use and highly customizable Prometheus exporter
for AWS Cost and Usage Reports

**Project status: *alpha***. All planned features are completed.

**NOTICE: This project is not affiliated, associated, authorized, endorsed by, or in any way officially connected with Amazon, Amazon Web Services (AWS), or any of its subsidiaries or its affiliates. This project will soon be renamed to `cloud-cost-exporter`.**

### [Homepage](https://github.com/st8ed/aws-cost-exporter)
### [Example](https://raw.githubusercontent.com/st8ed/aws-cost-exporter/gh-pages/assets/demo.png)

## Deployment

Report delivery to S3 bucket should be [configured](#setup-aws) prior deployment.

Examples below use following shell variables:

```bash
export AWS_ACCESS_KEY_ID=your-key
export AWS_SECRET_ACCESS_KEY=your-secret-key
export AWS_REGION=us-east-1

report_bucket=your-bucket-name
report_name=your-report-name
```

Deploy with Docker:
```bash
docker run --rm \
    -p 127.0.0.1:9100:9100 \
    -e AWS_ACCESS_KEY_ID \
    -e AWS_SECRET_ACCESS_KEY \
    -e AWS_REGION \
    st8ed/aws-cost-exporter \
    --bucket $report_bucket --report $report_name
```

Deploy with Helm (preferred):
```bash
helm repo add aws-cost-exporter https://st8ed.github.io/aws-cost-exporter/
helm repo update

# Note: prefer to use -f values.yaml because commandline arguments
# are considered insecure (exposed systemwide)
helm install \
    aws-cost-exporter aws-cost-exporter/aws-cost-exporter \
    --set "aws.access_key_id=$AWS_ACCESS_KEY_ID" \
    --set "aws.secret_access_key=$AWS_SECRET_ACCESS_KEY" \
    --set "aws.region=$AWS_REGION" \
    --set "aws.bucket=$report_bucket" \
    --set "aws.report=$report_name"
```

Deploy locally:
```bash
aws-cost-exporter \
    --bucket $report_bucket \
    --report $report_name
```

## Usage

Exported metrics are labelled according to [SQL query](https://github.com/st8ed/aws-cost-exporter/blob/main/configs/queries/common.sql), which itself
is configurable. See [original column descriptions](https://docs.aws.amazon.com/cur/latest/userguide/data-dictionary.html) for details.

```
usage: aws-cost-exporter --bucket=BUCKET --report=REPORT [<flags>]

Flags:
  -h, --help               Show context-sensitive help (also try --help-long and
                           --help-man).
      --bucket=BUCKET      Name of the S3 bucket with detailed billing report(s)
      --report=REPORT      Name of the AWS detailed billing report in supplied
                           S3 bucket
      --repository="/var/lib/aws-cost-exporter/repository"
                           Path to store cached AWS billing reports
      --queries-dir="/etc/aws-cost-exporter/queries"
                           Path to directory with SQL queries for gathering
                           metrics
      --state-path="/var/lib/aws-cost-exporter/state.json"
                           Path to store exporter state
      --web.listen-address=":9100"
                           Address on which to expose metrics and web interface.
      --web.telemetry-path="/metrics"
                           Path under which to expose metrics.
      --web.disable-exporter-metrics
                           Exclude metrics about the exporter itself
                           (promhttp_*, process_*, go_*).
      --web.config=""      [EXPERIMENTAL] Path to config yaml file that can
                           enable TLS or authentication.
      --log.level=info     Only log messages with the given severity or above.
                           One of: [debug, info, warn, error]
      --log.format=logfmt  Output format of log messages. One of: [logfmt, json]
      --version            Show application version.
```

## Setup AWS
Create a Cost and Usage report following official [instructions](https://docs.aws.amazon.com/cur/latest/userguide/cur-create.html). Alternatively you can setup AWS resources [using AWS CLI](#configure-with-aws-cli). Some configuration values are necessary for the exporter to work:

- Report data integration must be **disabled**, so reports are gzipped csv files
- "Hourly" or "Daily" time granularity
- Minimal prefix is chosen (`/`)

You also need to setup proper IAM user with read access to S3 bucket.

## Setup AWS with AWS CLI
Equivalently you can create a report using AWS CLI (see `aws cur put-report-definition`). An example of suitable report definition:

```json
{
    "ReportDefinitions": [
        {
            "ReportName": "your-report-name",
            "TimeUnit": "HOURLY",
            "Format": "textORcsv",
            "Compression": "GZIP",
            "AdditionalSchemaElements": [
                "RESOURCES"
            ],
            "S3Bucket": "your-bucket-name",
            "S3Prefix": "/",
            "S3Region": "us-east-1",
            "AdditionalArtifacts": [],
            "RefreshClosedReports": false,
            "ReportVersioning": "CREATE_NEW_REPORT"
        }
    ]
}
```

## How it works

Internally the exporter analyzes AWS CUR Manifest file in S3 bucket and locally synchronizes most recent CSV report file.
The synchronized files are stored indefinitely in path specified in `--repository`.

On each scrape, a HEAD request is performed to check if AWS CUR Manifest is updated.
After synchronization, a SQL query using `csvq` is ran. The query result is exported as metrics with `metric_` prefix marking the columns to export.


## How to build

[Nix](https://nixos.org/) is used for fully reproducible build.

```bash
# Build docker image
nix build .#dockerImage

# Build chart
nix build .#helmChart
```

## Alternatives

Most publicly available alternatives use AWS Cost Explorer API, which costs money
for each API request. AWS Cost and Usage Reports is free, only a very small fee is
charged for S3 usage.
