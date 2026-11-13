#!/bin/bash
# Run the acceptance tests against the instance created by rds_up.sh.
# By default only the tests of the role / database parameters are run,
# set RDS_TESTS to a go test -run pattern to run other ones.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/rds_common.sh"

if [ ! -f "$RDS_STATE" ]; then
    echo "$RDS_STATE not found, create the instance with rds_up.sh first" >&2
    exit 1
fi
set -a
source "$RDS_STATE"
set +a

export TF_ACC=true
export PGPORT=5432
export PGUSER=$RDS_USER
export PGSSLMODE=require
# The master user of an RDS instance is not a superuser.
export PGSUPERUSER=false
# The instance cannot connect to its own public address: for the subscription
# tests, where the publisher is the instance itself, it uses its local address.
export TEST_PUBLISHER_HOST=127.0.0.1

cd "$HERE/.."
# RDS_TEST_TIMEOUT: the whole suite needs several hours on a remote instance
go test ./postgresql -count=1 -v -timeout "${RDS_TEST_TIMEOUT:-60m}" -run "${RDS_TESTS:-Parameter}"
