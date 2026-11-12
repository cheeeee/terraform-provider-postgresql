#!/bin/bash
# End-to-end test of the role / database parameters with the Terraform CLI:
# the provider is built from the sources, the configuration of this directory
# is applied with different variables and the content of every plan is checked.
#
# The server is defined by the usual PGHOST, PGPORT, PGUSER, PGPASSWORD and
# PGSSLMODE variables, pgaudit has to be loaded on it. Use e2e_local.sh to run
# it against the local test database or rds_e2e.sh against an AWS RDS instance.
#
# Set E2E_TERRAFORM=tofu to run it with OpenTofu instead of Terraform.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
PGPORT=${PGPORT:-5432}
PGSSLMODE=${PGSSLMODE:-require}
TERRAFORM=${E2E_TERRAFORM:-terraform}

WORK=$(mktemp -d)
cleanup() {
    "$TERRAFORM" -chdir="$WORK/tf" destroy -input=false -auto-approve -var-file=audit-off.tfvars > /dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo "## $("$TERRAFORM" version | head -n 1)"
echo "## Building the provider"
(cd "$HERE/../.." && go build -o "$WORK/bin/terraform-provider-postgresql" .)
cat > "$WORK/terraformrc" <<RC
provider_installation {
  dev_overrides {
    "cyrilgdn/postgresql" = "$WORK/bin"
  }
  direct {}
}
RC
mkdir "$WORK/tf"
cp "$HERE"/main.tf "$HERE"/*.tfvars "$WORK/tf/"

export TF_CLI_CONFIG_FILE="$WORK/terraformrc"
export TF_IN_AUTOMATION=1
export TF_VAR_pg_host=$PGHOST TF_VAR_pg_port=$PGPORT TF_VAR_pg_user=$PGUSER TF_VAR_pg_password=$PGPASSWORD TF_VAR_pg_sslmode=$PGSSLMODE

tf() {
    "$TERRAFORM" -chdir="$WORK/tf" "$@"
}

# E2E_PSQL can be set to another command to run psql (see e2e_local.sh)
sql() {
    if [ -n "${E2E_PSQL:-}" ]; then
        $E2E_PSQL -v ON_ERROR_STOP=1 -qAt -c "$1"
    else
        PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=$PGPORT user=$PGUSER dbname=postgres sslmode=$PGSSLMODE" -v ON_ERROR_STOP=1 -qAt -c "$1"
    fi
}

# plan_check <name> <expected changes (see check_plan.py)> <terraform plan arguments>
plan_check() {
    local name=$1 expected=$2
    shift 2
    echo
    echo "## $name: $TERRAFORM plan $*"
    if ! tf plan -input=false -no-color -out=tfplan "$@" > "$WORK/plan.txt" 2>&1; then
        cat "$WORK/plan.txt"
        exit 1
    fi
    # Show the changes of the plan
    sed -n '/^  # /,/^Plan:/p;/^No changes/p' "$WORK/plan.txt"
    tf show -json tfplan | python3 "$HERE/check_plan.py" "$name" "$expected"
}

apply() {
    echo
    echo "## $TERRAFORM apply $*"
    tf apply -input=false -no-color -auto-approve "$@" | grep -E "^(Apply complete|Error)" || true
    # Nothing must be left to change after an apply
    plan_check "the plan is empty after the apply" '{}' "$@"
}

expect_sql() {
    local name=$1 expected=$2 actual
    actual=$(sql "$3")
    if [ "$actual" != "$expected" ]; then
        echo "FAIL: $name: expected '$expected', got '$actual'"
        exit 1
    fi
    echo "ok: $name"
}

ROLE_CONFIG="SELECT array_to_string(ARRAY(SELECT x FROM unnest(rolconfig) AS x ORDER BY x COLLATE \"C\"), '; ') FROM pg_roles WHERE rolname = 'e2e_app'"
DB_CONFIG="SELECT array_to_string(ARRAY(SELECT x FROM unnest(setconfig) AS x ORDER BY x COLLATE \"C\"), '; ') FROM pg_db_role_setting s JOIN pg_database d ON d.oid = s.setdatabase WHERE d.datname = 'e2e_app' AND s.setrole = 0"

DB_ON='{"pgaudit.log": "ddl, write", "search_path": "\"$user\", public", "timezone": "UTC"}'
DB_OFF='{"pgaudit.log": "none", "search_path": "\"$user\", public", "timezone": "UTC"}'

plan_check "creation with the audit enabled (audit-on.tfvars)" '{
  "postgresql_role.app":     {"actions": ["create"], "after": {"pgaudit.log": "ddl, write", "work_mem": "64MB"}},
  "postgresql_database.app": {"actions": ["create"], "after": '"$DB_ON"'}
}' -var-file=audit-on.tfvars
apply -var-file=audit-on.tfvars
expect_sql "parameters of the role in the database" 'pgaudit.log=ddl, write; work_mem=64MB' "$ROLE_CONFIG"
expect_sql "parameters of the database in the database" 'TimeZone=UTC; pgaudit.log=ddl, write; search_path="$user", public' "$DB_CONFIG"

plan_check "audit disabled (audit-off.tfvars): only pgaudit.log changes" '{
  "postgresql_role.app":     {"actions": ["update"], "before": {"pgaudit.log": "ddl, write", "work_mem": "64MB"}, "after": {"pgaudit.log": "none", "work_mem": "64MB"}},
  "postgresql_database.app": {"actions": ["update"], "before": '"$DB_ON"', "after": '"$DB_OFF"'}
}' -var-file=audit-off.tfvars
apply -var-file=audit-off.tfvars
expect_sql "audit disabled for the role" 'pgaudit.log=none; work_mem=64MB' "$ROLE_CONFIG"

plan_check "variables of the command line override the file (-var)" '{
  "postgresql_role.app":     {"actions": ["update"], "after": {"pgaudit.log": "all", "work_mem": "64MB"}},
  "postgresql_database.app": {"actions": ["update"], "after": {"pgaudit.log": "all", "search_path": "\"$user\", public", "timezone": "UTC"}}
}' -var-file=audit-off.tfvars -var audit_enabled=true -var audit_classes=all

echo
echo "## Changes made outside of Terraform"
sql "ALTER ROLE e2e_app SET pgaudit.log TO 'all'"
sql "ALTER ROLE e2e_app SET lock_timeout TO '5s'"
sql "ALTER DATABASE e2e_app RESET timezone"
plan_check "changes made outside of Terraform are reported" '{
  "postgresql_role.app":     {"actions": ["update"], "before": {"lock_timeout": "5s", "pgaudit.log": "all", "work_mem": "64MB"}, "after": {"pgaudit.log": "none", "work_mem": "64MB"}},
  "postgresql_database.app": {"actions": ["update"], "before": {"pgaudit.log": "none", "search_path": "\"$user\", public"}, "after": '"$DB_OFF"'}
}' -var-file=audit-off.tfvars
apply -var-file=audit-off.tfvars
expect_sql "changes made outside of Terraform are reverted for the role" 'pgaudit.log=none; work_mem=64MB' "$ROLE_CONFIG"
expect_sql "changes made outside of Terraform are reverted for the database" 'TimeZone=UTC; pgaudit.log=none; search_path="$user", public' "$DB_CONFIG"

echo
echo "## $TERRAFORM destroy"
tf destroy -input=false -no-color -auto-approve -var-file=audit-off.tfvars | grep -E "^(Destroy complete|Error)" || true
expect_sql "the role and the database are deleted" '0' "SELECT (SELECT count(*) FROM pg_roles WHERE rolname = 'e2e_app') + (SELECT count(*) FROM pg_database WHERE datname = 'e2e_app')"

echo
echo "All the end-to-end checks passed"
