#!/bin/bash
# Upgrade test: a configuration with one resource of every type (tests/e2e/upgrade)
# is applied with a RELEASED version of the provider, then the provider built from
# the sources takes over the same state. Nothing must be planned: an upgrade of the
# provider never changes what is already deployed.
#
# It is run for the previous release of this fork and for the provider it is forked
# from (the state is then moved with "state replace-provider", like a user would do).
#
# The server is defined by PGHOST, PGPORT, PGUSER, PGPASSWORD and PGSSLMODE; the user
# has to be a superuser and the server needs wal_level = logical, the test_decoding
# plugin and the dummy_seclabel provider: it is the local test database (e2e_local.sh).
# UPGRADE_PUBLISHER_CONNINFO is how the server reaches itself (default: its own port 5432).
#
# Set E2E_TERRAFORM=tofu to run it with OpenTofu instead of Terraform.
# UPGRADE_FROM can be set to a list of "<source>=<version constraint>".
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
PGPORT=${PGPORT:-5432}
PGSSLMODE=${PGSSLMODE:-require}
TERRAFORM=${E2E_TERRAFORM:-terraform}
UPGRADE_FROM=${UPGRADE_FROM:-"cheeeee/postgresql=1.28.0 cyrilgdn/postgresql=>=1.26.0"}
PUBLISHER_CONNINFO=${UPGRADE_PUBLISHER_CONNINFO:-"host=localhost port=5432 user=$PGUSER password=$PGPASSWORD dbname=upg_data"}
# The released providers are downloaded from the Terraform registry, with OpenTofu too
REGISTRY=registry.terraform.io
FORK=$REGISTRY/cheeeee/postgresql

WORK=$(mktemp -d)

# E2E_PSQL can be set to another command to run psql (see e2e_local.sh)
sql() {
    local database=$1
    shift
    if [ -n "${E2E_PSQL:-}" ]; then
        $E2E_PSQL -d "$database" -v ON_ERROR_STOP=1 -qAt -c "$1"
    else
        PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=$PGPORT user=$PGUSER dbname=$database sslmode=$PGSSLMODE" -v ON_ERROR_STOP=1 -qAt -c "$1"
    fi
}

# What is not managed by the configuration
drop_fixtures() {
    sql postgres "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = 'upg_sub' AND NOT active" > /dev/null
    sql postgres "DROP DATABASE IF EXISTS upg_data"
    sql postgres "DROP ROLE IF EXISTS upg_member"
}

cleanup() {
    if [ -d "$WORK/tf" ]; then
        "$TERRAFORM" -chdir="$WORK/tf" destroy -input=false -auto-approve > /dev/null 2>&1 || true
    fi
    drop_fixtures > /dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

export TF_IN_AUTOMATION=1
export TF_VAR_pg_host=$PGHOST TF_VAR_pg_port=$PGPORT TF_VAR_pg_user=$PGUSER TF_VAR_pg_password=$PGPASSWORD TF_VAR_pg_sslmode=$PGSSLMODE
export TF_VAR_publisher_conninfo=$PUBLISHER_CONNINFO

tf() {
    "$TERRAFORM" -chdir="$WORK/tf" "$@"
}

# use_provider <source> [<version constraint>]: the provider required by the configuration
use_provider() {
    local version=""
    if [ -n "${2:-}" ]; then
        version="version = \"$2\""
    fi
    cat > "$WORK/tf/versions.tf" <<TF
terraform {
  required_providers {
    postgresql = {
      source = "$1"
      $version
    }
  }
}
TF
}

# expect_no_change <name>: the plan must be empty
expect_no_change() {
    local name=$1 status=0
    tf plan -input=false -no-color -detailed-exitcode > "$WORK/plan.txt" 2>&1 || status=$?
    if [ "$status" != 0 ]; then
        cat "$WORK/plan.txt"
        echo "FAIL: $name"
        exit 1
    fi
    echo "ok: $name"
}

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

for from in $UPGRADE_FROM; do
    source=$REGISTRY/${from%%=*}
    constraint=${from#*=}

    echo
    echo "## Upgrade from $source ($constraint)"
    rm -rf "$WORK/tf"
    mkdir "$WORK/tf"
    cp "$HERE/upgrade/main.tf" "$WORK/tf/"
    if [ "$source" = "$FORK" ]; then
        cp "$HERE/upgrade/fork.tf" "$WORK/tf/"
    fi

    drop_fixtures
    sql postgres "CREATE DATABASE upg_data"
    sql postgres "CREATE ROLE upg_member"
    sql upg_data "CREATE TABLE upg_items (id serial PRIMARY KEY, name text)"
    sql upg_data "SELECT pg_create_logical_replication_slot('upg_sub', 'pgoutput')" > /dev/null

    # --- the released provider
    unset TF_CLI_CONFIG_FILE
    use_provider "$source" "$constraint"
    tf init -input=false -no-color > "$WORK/init.txt" 2>&1 || { cat "$WORK/init.txt"; exit 1; }
    echo "## $(grep -E "Installed .*postgresql" "$WORK/init.txt" | sed 's/^- //')"
    tf apply -input=false -no-color -auto-approve > "$WORK/apply.txt" 2>&1 || { cat "$WORK/apply.txt"; exit 1; }
    grep -E "^Apply complete" "$WORK/apply.txt"
    expect_no_change "the plan of the released provider is empty"

    # --- the provider built from the sources takes over
    if [ "$source" != "$FORK" ]; then
        tf state replace-provider -auto-approve "$source" "$FORK" > /dev/null
    fi
    use_provider "$FORK"
    export TF_CLI_CONFIG_FILE="$WORK/dev.tfrc"
    expect_no_change "the plan of the new provider is empty"
    tf apply -input=false -no-color -auto-approve > "$WORK/apply.txt" 2>&1 || { cat "$WORK/apply.txt"; exit 1; }
    grep -E "^Apply complete" "$WORK/apply.txt"
    expect_no_change "the plan is still empty after an apply of the new provider"

    tf destroy -input=false -no-color -auto-approve > "$WORK/destroy.txt" 2>&1 || { cat "$WORK/destroy.txt"; exit 1; }
    grep -E "^Destroy complete" "$WORK/destroy.txt"
    left=$(sql postgres "SELECT (SELECT count(*) FROM pg_roles WHERE rolname LIKE 'upg\\_%' AND rolname <> 'upg_member') + (SELECT count(*) FROM pg_database WHERE datname IN ('upg_db', 'upg_audited')) + (SELECT count(*) FROM pg_replication_slots WHERE slot_name IN ('upg_logical', 'upg_physical'))")
    if [ "$left" != 0 ]; then
        echo "FAIL: $left objects are left after the destroy of the new provider"
        exit 1
    fi
    echo "ok: the new provider destroys what the released provider created"
done

echo
echo "All the upgrade checks passed"
