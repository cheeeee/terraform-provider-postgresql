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
    if [ -d "$WORK/app" ]; then
        TF_VAR_passwords='{}' "$TERRAFORM" -chdir="$WORK/app" destroy -input=false -auto-approve -var audit=none > /dev/null 2>&1 || true
    fi
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
    "cheeeee/postgresql"  = "$WORK/bin"
  }
  direct {}
}
RC
mkdir "$WORK/tf"
cp "$HERE"/main.tf "$HERE"/*.tfvars "$WORK/tf/"

export TF_CLI_CONFIG_FILE="$WORK/terraformrc"
export TF_IN_AUTOMATION=1
export TF_VAR_pg_host=$PGHOST TF_VAR_pg_port=$PGPORT TF_VAR_pg_user=$PGUSER TF_VAR_pg_password=$PGPASSWORD TF_VAR_pg_sslmode=$PGSSLMODE

# Directory of the configuration used by tf
TFDIR="$WORK/tf"

tf() {
    "$TERRAFORM" -chdir="$TFDIR" "$@"
}

# E2E_PSQL can be set to another command to run psql (see e2e_local.sh)
sql() {
    if [ -n "${E2E_PSQL:-}" ]; then
        $E2E_PSQL -v ON_ERROR_STOP=1 -qAt -c "$1"
    else
        PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=$PGPORT user=$PGUSER dbname=postgres sslmode=$PGSSLMODE" -v ON_ERROR_STOP=1 -qAt -c "$1"
    fi
}

# sql_as <role> <password> <database> <query>: run a query as another role
sql_as() {
    if [ -n "${E2E_PSQL:-}" ]; then
        $E2E_PSQL -U "$1" -d "$3" -v ON_ERROR_STOP=1 -qAt -c "$4"
    else
        PGPASSWORD=$2 psql "host=$PGHOST port=$PGPORT user=$1 dbname=$3 sslmode=$PGSSLMODE" -v ON_ERROR_STOP=1 -qAt -c "$4"
    fi
}

# expect_as <name> <expected> <role> <password> <database> <query>
expect_as() {
    local name=$1 expected=$2 actual
    shift 2
    actual=$(sql_as "$@")
    if [ "$actual" != "$expected" ]; then
        echo "FAIL: $name: expected '$expected', got '$actual'"
        exit 1
    fi
    echo "ok: $name"
}

# expect_denied <name> <role> <password> <database> <query>: the query must be rejected
expect_denied() {
    local name=$1
    shift
    if sql_as "$@" > /dev/null 2>&1; then
        echo "FAIL: $name: the query was accepted"
        exit 1
    fi
    echo "ok: $name"
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

# --- The module examples/app-database: a database for an application which creates its tables itself

echo
if ! "$TERRAFORM" version -json | python3 -c 'import json, sys; v = json.load(sys.stdin)["terraform_version"].split("."); sys.exit(0 if (int(v[0]), int(v[1])) >= (1, 11) else 1)'; then
    echo "## Module examples/app-database: SKIPPED, it needs Terraform or OpenTofu >= 1.11 (write-only passwords)"
    echo
    echo "All the end-to-end checks passed"
    exit 0
fi

echo "## Module examples/app-database"
mkdir "$WORK/app"
cp "$HERE/app-database/main.tf" "$WORK/app/"
cp -R "$HERE/../../examples/app-database" "$WORK/app/app-database"
TFDIR="$WORK/app"
tf get > /dev/null

# The passwords are given with an ephemeral variable: they must not be in the state
export TF_VAR_passwords='{"e2e_orders_migrator": "migrator-password", "e2e_orders_rw": "rw-password", "e2e_orders_ro": "ro-password", "e2e_orders_ro2": "ro2-password", "e2e_orders_noaudit": "noaudit-password"}'

APP_ON='{"pgaudit.log": "ddl, write", "statement_timeout": "30s"}'
APP_OFF='{"pgaudit.log": "none", "statement_timeout": "30s"}'

plan_check "the module creates the database, the roles, the privileges and the extension" '{
  "module.orders.postgresql_database.this":                         {"actions": ["create"], "after": '"$APP_ON"'},
  "module.orders.postgresql_role.user[\"e2e_orders_noaudit\"]":     {"actions": ["create"], "after": {"pgaudit.log": "none"}},
  "*": {"actions": ["create"], "count": 28}
}' -var "audit=ddl, write"
apply -var "audit=ddl, write"

if grep -q -e "-password" "$WORK/app/terraform.tfstate"; then
    echo "FAIL: a password is stored in the state"
    exit 1
fi
echo "ok: the passwords are not stored in the state"

echo
echo "## The application creates its tables and uses them"
sql_as e2e_orders_migrator migrator-password e2e_orders "CREATE TABLE orders (id serial PRIMARY KEY, item text)"
echo "ok: the migrator creates a table"
expect_sql "the database belongs to the owner role" 'e2e_orders_owner' "SELECT rolname FROM pg_roles WHERE oid = (SELECT datdba FROM pg_database WHERE datname = 'e2e_orders')"
expect_as "the table created by the migrator belongs to the owner" 'e2e_orders_owner' e2e_orders_rw rw-password e2e_orders "SELECT tableowner FROM pg_tables WHERE tablename = 'orders'"
sql_as e2e_orders_rw rw-password e2e_orders "INSERT INTO orders (item) VALUES ('book')"
echo "ok: the read-write role inserts a row in a table created after the module was applied"
expect_as "the read-only role reads the table" '1' e2e_orders_ro ro-password e2e_orders "SELECT count(*) FROM orders"
expect_denied "the read-only role cannot write" e2e_orders_ro ro-password e2e_orders "INSERT INTO orders (item) VALUES ('pen')"
expect_denied "the read-write role cannot create a table" e2e_orders_rw rw-password e2e_orders "CREATE TABLE other (id int)"
expect_denied "the read-write role cannot drop a table" e2e_orders_rw rw-password e2e_orders "DROP TABLE orders"
expect_as "the sessions of the database are audited" 'ddl, write' e2e_orders_rw rw-password e2e_orders "SHOW pgaudit.log"
expect_as "the role with its own setting is not audited" 'none' e2e_orders_noaudit noaudit-password e2e_orders "SHOW pgaudit.log"
expect_as "the extension is available" 't' e2e_orders_rw rw-password e2e_orders "SELECT length(gen_random_bytes(4)) = 4"
expect_sql "the other roles of the server cannot connect to the database" 'f' "SELECT has_database_privilege('public', 'e2e_orders', 'CONNECT')"

echo
echo "## A read-only role is added to the database which is already used, then removed"
MORE_ROLES='{"e2e_orders_ro": {}, "e2e_orders_noaudit": {"audit": "none"}, "e2e_orders_ro2": {}}'
apply -var "audit=ddl, write" -var "roles=$MORE_ROLES"
expect_as "the new read-only role reads the existing table" '1' e2e_orders_ro2 ro2-password e2e_orders "SELECT count(*) FROM orders"
expect_denied "the new read-only role cannot write" e2e_orders_ro2 ro2-password e2e_orders "INSERT INTO orders (item) VALUES ('pen')"
apply -var "audit=ddl, write"
expect_sql "the removed role is deleted" '0' "SELECT count(*) FROM pg_roles WHERE rolname = 'e2e_orders_ro2'"

echo
echo "## The passwords are changed"
export TF_VAR_passwords='{"e2e_orders_migrator": "migrator-password-2", "e2e_orders_rw": "rw-password-2", "e2e_orders_ro": "ro-password-2", "e2e_orders_noaudit": "noaudit-password-2"}'
plan_check "a new password is not applied while its version is the same" '{}' -var "audit=ddl, write"
plan_check "a new version of the passwords updates the roles" '{
  "*": {"actions": ["update"], "count": 4}
}' -var "audit=ddl, write" -var password_version=2
apply -var "audit=ddl, write" -var password_version=2
expect_as "the role logs in with its new password" '1' e2e_orders_ro ro-password-2 e2e_orders "SELECT count(*) FROM orders"
if [ -z "${E2E_PSQL:-}" ]; then
    # (the passwords are not checked through the local socket used by E2E_PSQL)
    expect_denied "the old password is rejected" e2e_orders_ro ro-password e2e_orders "SELECT 1"
fi

plan_check "audit disabled with the variable of the module: only pgaudit.log changes" '{
  "module.orders.postgresql_database.this": {"actions": ["update"], "before": '"$APP_ON"', "after": '"$APP_OFF"'}
}' -var audit=none -var password_version=2
apply -var audit=none -var password_version=2

echo
echo "## $TERRAFORM destroy"
tf destroy -input=false -no-color -auto-approve -var audit=none -var password_version=2 | grep -E "^(Destroy complete|Error)" || true
expect_sql "the database and the roles of the module are deleted" '0' "SELECT (SELECT count(*) FROM pg_roles WHERE rolname LIKE 'e2e\\_orders%') + (SELECT count(*) FROM pg_database WHERE datname = 'e2e_orders')"

echo
echo "All the end-to-end checks passed"
