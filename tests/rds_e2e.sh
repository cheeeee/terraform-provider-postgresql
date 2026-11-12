#!/bin/bash
# Run the Terraform end-to-end test (tests/e2e) against the instance created by rds_up.sh.
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

export PGPORT=5432
export PGUSER=$RDS_USER
export PGSSLMODE=require

"$HERE/e2e/run.sh"

# What only exists on AWS: IAM authentication and a password from Secrets Manager
export RDS_SECRET
"$HERE/e2e/rds_secret.sh"
