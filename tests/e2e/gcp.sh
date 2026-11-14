#!/bin/bash
# End-to-end test which only makes sense on Google Cloud SQL: the provider connects
# through the Cloud SQL proxy (scheme = "gcppostgres"), which nothing else tests:
#   - with the password of a user, the instance written as project:region:instance (the
#     connection name given by google_sql_database_instance) and as project/region/instance;
#   - with the IAM database authentication of a service account which the provider
#     impersonates (gcp_iam_impersonate_service_account).
#
# It needs the credentials of Google Cloud of the environment (the application default
# credentials) and:
#   PGHOST, PGPASSWORD            public address of the instance and password of GCP_USER
#   GCP_PROJECT, GCP_NAME         project and name of the instance
#   GCP_CONNECTION_NAME           project:region:instance
#   GCP_CONNECT_SERVICE_ACCOUNT   service account to impersonate: the credentials have to be
#                                 allowed to impersonate it, and it has to be allowed to
#                                 connect and to log in to the instance (tests/gcp/setup.sh)
# Everything it creates is deleted at the end (and with the instance).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
: "${GCP_PROJECT:?GCP_PROJECT must be set}"
: "${GCP_NAME:?GCP_NAME must be set}"
: "${GCP_CONNECTION_NAME:?GCP_CONNECTION_NAME must be set}"
: "${GCP_CONNECT_SERVICE_ACCOUNT:?GCP_CONNECT_SERVICE_ACCOUNT must be set}"
PGUSER=${GCP_USER:-postgres}
TERRAFORM=${E2E_TERRAFORM:-terraform}
FORK=registry.terraform.io/cheeeee/postgresql
# The role of the database of a service account is its email without .gserviceaccount.com
IAM_USER=${GCP_CONNECT_SERVICE_ACCOUNT%.gserviceaccount.com}

WORK=$(mktemp -d)

sql() {
    PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=5432 user=$PGUSER dbname=postgres sslmode=require" -v ON_ERROR_STOP=1 -qAt -c "$1"
}

cleanup() {
    status=$?
    set +e
    if [ -d "$WORK/tf" ]; then
        tf destroy -input=false -auto-approve > /dev/null 2>&1
    fi
    sql "DROP ROLE IF EXISTS tfpg_gcp_probe" > /dev/null 2>&1
    rm -rf "$WORK"
    exit "$status"
}
trap cleanup EXIT

echo "## $("$TERRAFORM" version | head -n 1)"
echo "## Building the provider"
(cd "$HERE/../.." && go build -o "$WORK/bin/terraform-provider-postgresql" .)
cat > "$WORK/dev.tfrc" <<RC
provider_installation {
  dev_overrides {
    "$FORK" = "$WORK/bin"
  }
  direct {}
}
RC
export TF_CLI_CONFIG_FILE="$WORK/dev.tfrc"
export TF_IN_AUTOMATION=1

mkdir "$WORK/tf"
cp "$HERE/gcp/main.tf" "$WORK/tf/"
cat > "$WORK/tf/versions.tf" <<TF
terraform {
  required_providers {
    postgresql = {
      source = "$FORK"
    }
  }
}
TF

tf() {
    "$TERRAFORM" -chdir="$WORK/tf" "$@"
}

# expect_login <name> <variables>: the provider connects, creates a role, has an empty plan
# and deletes the role
expect_login() {
    local name=$1 attempt
    shift
    # what was just allowed in IAM takes some time to be known
    for attempt in $(seq 12); do
        if tf apply -input=false -no-color -auto-approve "$@" > "$WORK/apply.txt" 2>&1; then
            break
        fi
        if [ "$attempt" = 12 ] || ! grep -qiE "not authorized|permission|IAM|authentication failed|403" "$WORK/apply.txt"; then
            cat "$WORK/apply.txt"
            echo "FAIL: $name"
            exit 1
        fi
        sleep 10
    done
    if [ "$(sql "SELECT count(*) FROM pg_roles WHERE rolname = 'tfpg_gcp_probe'")" != 1 ]; then
        echo "FAIL: $name: the role was not created"
        exit 1
    fi
    if ! tf plan -input=false -no-color -detailed-exitcode "$@" > "$WORK/plan.txt" 2>&1; then
        cat "$WORK/plan.txt"
        echo "FAIL: $name: the plan is not empty"
        exit 1
    fi
    tf destroy -input=false -no-color -auto-approve "$@" > "$WORK/destroy.txt" 2>&1 || { cat "$WORK/destroy.txt"; exit 1; }
    echo "ok: $name"
}

echo "## Provider connecting through the Cloud SQL proxy"
sql "DROP ROLE IF EXISTS tfpg_gcp_probe"

expect_login "password, the instance as project:region:instance" \
    -var "host=$GCP_CONNECTION_NAME" -var "pg_user=$PGUSER" -var "pg_password=$PGPASSWORD"
expect_login "password, the instance as project/region/instance" \
    -var "host=${GCP_CONNECTION_NAME//://}" -var "pg_user=$PGUSER" -var "pg_password=$PGPASSWORD"

echo "## IAM database authentication of an impersonated service account"
if ! gcloud --project "$GCP_PROJECT" sql users list --instance "$GCP_NAME" --format 'value(name)' | grep -qxF "$IAM_USER"; then
    gcloud --project "$GCP_PROJECT" sql users create "$IAM_USER" --instance "$GCP_NAME" --type cloud_iam_service_account > /dev/null
fi
sql "ALTER ROLE \"$IAM_USER\" CREATEROLE"
expect_login "impersonated service account, no password" \
    -var "host=$GCP_CONNECTION_NAME" -var "pg_user=$IAM_USER" -var "impersonate=$GCP_CONNECT_SERVICE_ACCOUNT"

echo
echo "All the Cloud SQL checks passed"
