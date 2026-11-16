#!/bin/bash
# Run the acceptance tests against the server created by azure_up.sh.
# AZURE_TESTS is a go test -run pattern (the whole suite without it), AZURE_SKIP a go test
# -skip pattern (see rds_matrix.py).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/azure_common.sh"

if [ ! -f "$AZURE_STATE" ]; then
    echo "$AZURE_STATE not found, create the server with azure_up.sh first" >&2
    exit 1
fi
set -a
source "$AZURE_STATE"
set +a

export TF_ACC=true
export PGPORT=5432
export PGUSER=$AZURE_USER
export PGSSLMODE=require
# The administrator of a flexible server is not a superuser.
export PGSUPERUSER=false
# The publisher of the subscription tests is the server itself, at its local address.
export TEST_PUBLISHER_HOST=127.0.0.1

cd "$HERE/.."
skip=()
if [ -n "${AZURE_SKIP:-}" ]; then
    skip=(-skip "$AZURE_SKIP")
fi
go test ./postgresql -count=1 -v -timeout "${AZURE_TEST_TIMEOUT:-60m}" -run "${AZURE_TESTS:-.}" ${skip[@]+"${skip[@]}"}
