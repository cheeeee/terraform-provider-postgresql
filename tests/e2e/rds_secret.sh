#!/bin/bash
# End-to-end test which only makes sense on AWS RDS: the examples/app-database
# module with the roles of the application using IAM authentication, and a role
# which is not audited with a password read from AWS Secrets Manager.
#
# It needs the same variables as run.sh, AWS credentials and RDS_SECRET, the
# name of the secret to create (it is deleted at the end).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
: "${AWS_REGION:?AWS_REGION must be set}"
: "${RDS_SECRET:?RDS_SECRET must be set}"
TERRAFORM=${E2E_TERRAFORM:-terraform}

# The AWS provider reads the credentials from the environment
eval "$(aws configure export-credentials --format env)"

WORK=$(mktemp -d)
tf() {
    "$TERRAFORM" -chdir="$WORK/tf" "$@"
}
cleanup() {
    tf destroy -input=false -auto-approve > /dev/null 2>&1 || true
    aws secretsmanager delete-secret --secret-id "$RDS_SECRET" --force-delete-without-recovery > /dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo "## $("$TERRAFORM" version | head -n 1)"
echo "## Building the provider"
# The provider is installed from a local mirror, and the AWS provider from its registry
registry=registry.terraform.io
if [ "$(basename "$TERRAFORM")" = "tofu" ]; then
    registry=registry.opentofu.org
fi
target="$WORK/mirror/$registry/cheeeee/postgresql/0.0.1/$(go env GOOS)_$(go env GOARCH)"
mkdir -p "$target"
(cd "$HERE/../.." && go build -o "$target/terraform-provider-postgresql_v0.0.1" .)
cat > "$WORK/terraformrc" <<RC
provider_installation {
  filesystem_mirror {
    path    = "$WORK/mirror"
    include = ["cheeeee/postgresql"]
  }
  direct {
    exclude = ["cheeeee/postgresql"]
  }
}
RC
export TF_CLI_CONFIG_FILE="$WORK/terraformrc"
export TF_IN_AUTOMATION=1

mkdir "$WORK/tf"
cp "$HERE/rds-secret/main.tf" "$WORK/tf/"
cp -R "$HERE/../../examples/app-database" "$WORK/tf/app-database"

export TF_VAR_pg_host=$PGHOST TF_VAR_pg_user=$PGUSER TF_VAR_pg_password=$PGPASSWORD
export TF_VAR_aws_region=$AWS_REGION TF_VAR_secret_id=$RDS_SECRET

# query_as <role> <password> <query>, in the database of the module
query_as() {
    PGPASSWORD=$2 PGCONNECT_TIMEOUT=30 psql "host=$PGHOST port=5432 user=$1 dbname=e2e_iam sslmode=require" -v ON_ERROR_STOP=1 -qAt -c "$3"
}
expect() {
    local name=$1 expected=$2 actual attempt
    shift 2
    # The login with an IAM token is sometimes slow on a small instance which has just run
    # the tests: a connection which times out is tried again.
    for attempt in 1 2 3; do
        if actual=$(query_as "$@" 2> "$WORK/psql.txt"); then
            break
        fi
        if [ "$attempt" = 3 ] || ! grep -q "timeout expired" "$WORK/psql.txt"; then
            cat "$WORK/psql.txt"
            echo "FAIL: $name"
            exit 1
        fi
    done
    if [ "$actual" != "$expected" ]; then
        echo "FAIL: $name: expected '$expected', got '$actual'"
        exit 1
    fi
    echo "ok: $name"
}
denied() {
    local name=$1
    shift
    if query_as "$@" > /dev/null 2>&1; then
        echo "FAIL: $name: the connection was accepted"
        exit 1
    fi
    echo "ok: $name"
}

echo
echo "## A password in AWS Secrets Manager"
password=$(openssl rand -hex 16)
aws secretsmanager create-secret --name "$RDS_SECRET" --secret-string "$password" \
    --tags Key=purpose,Value=tfpg-test > /dev/null
echo "ok: the secret $RDS_SECRET is created"

echo
echo "## $TERRAFORM init and apply"
tf init -input=false -no-color > "$WORK/init.txt" 2>&1 || { cat "$WORK/init.txt"; exit 1; }
tf apply -input=false -no-color -auto-approve | grep -E "^(Apply complete|Error)" || true
tf plan -input=false -no-color -detailed-exitcode > "$WORK/plan.txt" 2>&1 || { cat "$WORK/plan.txt"; echo "FAIL: the plan is not empty after the apply"; exit 1; }
echo "ok: the plan is empty after the apply"

if grep -q "$password" "$WORK/tf/terraform.tfstate"; then
    echo "FAIL: the password of the secret is stored in the state"
    exit 1
fi
echo "ok: the password of the secret is not stored in the state"

echo
echo "## The role with the password of the secret"
expect "it logs in with the password of the secret and is not audited" 'none' e2e_iam_reporting "$password" "SHOW pgaudit.log"

echo
echo "## The roles with IAM authentication"
token=$(aws rds generate-db-auth-token --hostname "$PGHOST" --port 5432 --username e2e_iam_rw --region "$AWS_REGION")
expect "the read-write role logs in with an IAM token and is audited" 'ddl, write' e2e_iam_rw "$token" "SHOW pgaudit.log"
token=$(aws rds generate-db-auth-token --hostname "$PGHOST" --port 5432 --username e2e_iam_migrator --region "$AWS_REGION")
expect "the migrator logs in with an IAM token and creates a table" '' e2e_iam_migrator "$token" "CREATE TABLE orders (id serial PRIMARY KEY, item text)"
expect "the table belongs to the owner of the database" 'e2e_iam_owner' e2e_iam_reporting "$password" "SELECT tableowner FROM pg_tables WHERE tablename = 'orders'"
denied "the role with a password cannot log in with an IAM token" e2e_iam_reporting \
    "$(aws rds generate-db-auth-token --hostname "$PGHOST" --port 5432 --username e2e_iam_reporting --region "$AWS_REGION")" "SELECT 1"
expect "the role with the password of the secret reads the table" '0' e2e_iam_reporting "$password" "SELECT count(*) FROM orders"

echo
echo "## The secret is changed"
new_password=$(openssl rand -hex 16)
aws secretsmanager put-secret-value --secret-id "$RDS_SECRET" --secret-string "$new_password" > /dev/null
tf plan -input=false -no-color -detailed-exitcode > "$WORK/plan.txt" 2>&1 || { cat "$WORK/plan.txt"; echo "FAIL: the new secret must not change anything while the version of the password is the same"; exit 1; }
echo "ok: nothing changes while the version of the password is the same"
tf apply -input=false -no-color -auto-approve -var password_version=2 | grep -E "^(Apply complete|Error)" || true
expect "the role logs in with the new password" 'none' e2e_iam_reporting "$new_password" "SHOW pgaudit.log"
denied "the old password is rejected" e2e_iam_reporting "$password" "SELECT 1"

echo
echo "## $TERRAFORM destroy"
tf destroy -input=false -no-color -auto-approve -var password_version=2 | grep -E "^(Destroy complete|Error)" || true
left=$(PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=5432 user=$PGUSER dbname=postgres sslmode=require" -qAt -c "SELECT (SELECT count(*) FROM pg_roles WHERE rolname LIKE 'e2e\\_iam%') + (SELECT count(*) FROM pg_database WHERE datname = 'e2e_iam')")
if [ "$left" != "0" ]; then
    echo "FAIL: the database and the roles are not deleted"
    exit 1
fi
echo "ok: the database and the roles are deleted"

echo
echo "All the AWS checks passed"
