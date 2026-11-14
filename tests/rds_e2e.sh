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

# The connection with scheme = "awspostgres" (the certificate authorities of RDS)
"$HERE/e2e/rds_scheme.sh"

# The provider itself logging in with IAM authentication and an assumed role.
# It creates an IAM role (and an IAM user when the credentials are the ones of the
# root user): set RDS_IAM_TEST=0 to skip it when the credentials cannot do that.
if [ "${RDS_IAM_TEST:-1}" != 0 ]; then
    export RDS_NAME RDS_IAM_ROLE RDS_IAM_USER
    "$HERE/e2e/rds_iam.sh"
fi
