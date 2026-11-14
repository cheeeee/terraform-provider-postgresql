#!/bin/bash
# End-to-end test which only makes sense on AWS RDS: the provider connects with
# scheme = "awspostgres", which verifies the certificate of the instance against the
# certificate authorities of RDS (they are downloaded). Nothing else tests this scheme.
#
# It needs the same variables as run.sh (the master user of the instance).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
PGPORT=${PGPORT:-5432}
TERRAFORM=${E2E_TERRAFORM:-terraform}
FORK=registry.terraform.io/cheeeee/postgresql

WORK=$(mktemp -d)

sql() {
    PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=$PGPORT user=$PGUSER dbname=postgres sslmode=require" -v ON_ERROR_STOP=1 -qAt -c "$1"
}

tf() {
    "$TERRAFORM" -chdir="$WORK/tf" "$@"
}

cleanup() {
    status=$?
    set +e
    if [ -d "$WORK/tf" ]; then
        tf destroy -input=false -auto-approve > /dev/null 2>&1
    fi
    sql "DROP ROLE IF EXISTS tfpg_scheme_probe" > /dev/null 2>&1
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
export TF_VAR_pg_host=$PGHOST TF_VAR_pg_port=$PGPORT TF_VAR_pg_user=$PGUSER TF_VAR_pg_password=$PGPASSWORD

mkdir "$WORK/tf"
cat > "$WORK/tf/main.tf" <<TF
terraform {
  required_providers {
    postgresql = {
      source = "$FORK"
    }
  }
}

variable "pg_host" {
  type = string
}

variable "pg_port" {
  type = number
}

variable "pg_user" {
  type = string
}

variable "pg_password" {
  type      = string
  sensitive = true
}

provider "postgresql" {
  scheme    = "awspostgres"
  host      = var.pg_host
  port      = var.pg_port
  username  = var.pg_user
  password  = var.pg_password
  superuser = false
}

resource "postgresql_role" "probe" {
  name = "tfpg_scheme_probe"
}
TF

echo "## Provider connecting with scheme = \"awspostgres\""
sql "DROP ROLE IF EXISTS tfpg_scheme_probe"
tf apply -input=false -no-color -auto-approve > "$WORK/apply.txt" 2>&1 || { cat "$WORK/apply.txt"; echo "FAIL: the provider does not connect"; exit 1; }
if [ "$(sql "SELECT count(*) FROM pg_roles WHERE rolname = 'tfpg_scheme_probe'")" != 1 ]; then
    echo "FAIL: the role was not created"
    exit 1
fi
echo "ok: the provider connects with the certificate authorities of RDS, and creates a role"
if ! tf plan -input=false -no-color -detailed-exitcode > "$WORK/plan.txt" 2>&1; then
    cat "$WORK/plan.txt"
    echo "FAIL: the plan is not empty"
    exit 1
fi
echo "ok: the plan is empty"
tf destroy -input=false -no-color -auto-approve > "$WORK/destroy.txt" 2>&1 || { cat "$WORK/destroy.txt"; exit 1; }
echo "ok: the role is deleted"

echo
echo "All the awspostgres checks passed"
