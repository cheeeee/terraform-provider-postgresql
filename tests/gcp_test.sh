#!/bin/bash
# Run the acceptance tests against the instance created by gcp_up.sh.
# GCP_TESTS is a go test -run pattern (the whole suite without it), GCP_SKIP a go test -skip
# pattern (see rds_matrix.py).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/gcp_common.sh"

if [ ! -f "$GCP_STATE" ]; then
    echo "$GCP_STATE not found, create the instance with gcp_up.sh first" >&2
    exit 1
fi
set -a
source "$GCP_STATE"
set +a

export TF_ACC=true
export PGPORT=5432
export PGUSER=$GCP_USER
export PGSSLMODE=require
# The postgres user of a Cloud SQL instance is not a superuser.
export PGSUPERUSER=false
# The publisher of the subscription tests is the instance itself. Cloud SQL refuses a
# subscription to the loopback address: the instance uses its public address, where
# gcp_up.sh allowed the address its own connections come from.
export TEST_PUBLISHER_HOST=${GCP_PUBLISHER_HOST:-$PGHOST}

cd "$HERE/.."
skip=()
if [ -n "${GCP_SKIP:-}" ]; then
    skip=(-skip "$GCP_SKIP")
fi
go test ./postgresql -count=1 -v -timeout "${GCP_TEST_TIMEOUT:-60m}" -run "${GCP_TESTS:-.}" ${skip[@]+"${skip[@]}"}
