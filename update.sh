#!/usr/bin/env bash
set -ex

VERSION=0.3.3
CHART_VERSION=0.1.4

URL_BASE="https://github.com/st8ed/aws-cost-exporter/releases/download/v$VERSION"
CHART_FILE="aws-cost-exporter-chart-$CHART_VERSION.tgz"

(
    mkdir -p ./tmp
    cd ./tmp

    wget --no-clobber "$URL_BASE/$CHART_FILE"
    helm repo index . --merge ../index.yaml --url "$URL_BASE"

    tar xf "$CHART_FILE" aws-cost-exporter/Chart.yaml
    cat aws-cost-exporter/Chart.yaml <(echo -e "\n...") - <<EOT | gpg -u chartsigner@st8ed.com --clearsign >"$CHART_FILE".prov
files:
  ${CHART_FILE}: sha256:$(sha256sum "${CHART_FILE}" | cut -d' ' -f1)
EOT

    mv -f index.yaml ../
)
