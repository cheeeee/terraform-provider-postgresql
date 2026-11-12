#!/bin/bash
# Settings shared by the rds_*.sh scripts, which run the acceptance tests
# against a temporary AWS RDS instance (see .github/workflows/rds-test.yml).

: "${PGVERSION:?PGVERSION must be set to a PostgreSQL major version (e.g.: 17)}"

export AWS_REGION=${AWS_REGION:-ca-central-1}
export AWS_PAGER=""

# Name of the instance, of its parameter group and of its security group.
# RDS_SUFFIX allows several instances of the same version at the same time.
RDS_NAME=${RDS_NAME:-tfpg-test}-pg${PGVERSION}${RDS_SUFFIX:+-$RDS_SUFFIX}
# File where the connection settings of the instance are stored.
RDS_STATE=${RDS_STATE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/${RDS_NAME}.env}
export RDS_USER=tfadmin
# Secret of AWS Secrets Manager created by the end-to-end test.
RDS_SECRET=${RDS_NAME}-reporting
# IAM role and IAM user created by the test of the IAM authentication of the provider.
RDS_IAM_ROLE=${RDS_NAME}-connect
RDS_IAM_USER=${RDS_NAME}-assumer
