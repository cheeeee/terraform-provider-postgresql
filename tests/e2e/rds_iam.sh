#!/bin/bash
# End-to-end test which only makes sense on AWS RDS: the provider itself logs in with
# IAM authentication (aws_rds_iam_auth), with the credentials of an assumed role
# (aws_rds_iam_provider_role_arn), from an environment which has no AWS region.
#
# The region of the token used to be lost when a role was assumed: the database
# refused the token with "PAM authentication failed". It is checked that the released
# version v1.28.0 fails this way and that the provider built from the sources logs in,
# with the region given by aws_rds_iam_region or by the profile.
#
# It needs the same variables as run.sh (the master user), AWS credentials, and:
#   RDS_NAME      identifier of the instance
#   RDS_IAM_ROLE  name of the IAM role to create, allowed to connect to the database
#   RDS_IAM_USER  name of the IAM user to create when the current credentials are the
#                 ones of the root user of the account, which cannot assume a role
# Everything it creates is deleted at the end (and by rds_down.sh).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGUSER:?PGUSER must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
: "${AWS_REGION:?AWS_REGION must be set}"
: "${RDS_NAME:?RDS_NAME must be set}"
: "${RDS_IAM_ROLE:?RDS_IAM_ROLE must be set}"
: "${RDS_IAM_USER:?RDS_IAM_USER must be set}"
TERRAFORM=${E2E_TERRAFORM:-terraform}
REGION=$AWS_REGION
DB_USER=tfpg_iam_admin
FORK=registry.terraform.io/cheeeee/postgresql
RELEASED=1.28.0

WORK=$(mktemp -d)

sql() {
    PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=5432 user=$PGUSER dbname=postgres sslmode=require" -v ON_ERROR_STOP=1 -qAt -c "$1"
}

delete_iam() {
    if aws iam get-user --user-name "$RDS_IAM_USER" > /dev/null 2>&1; then
        for key in $(aws iam list-access-keys --user-name "$RDS_IAM_USER" --query 'AccessKeyMetadata[].AccessKeyId' --output text); do
            aws iam delete-access-key --user-name "$RDS_IAM_USER" --access-key-id "$key"
        done
        aws iam delete-user-policy --user-name "$RDS_IAM_USER" --policy-name assume > /dev/null 2>&1 || true
        aws iam delete-user --user-name "$RDS_IAM_USER"
    fi
    if aws iam get-role --role-name "$RDS_IAM_ROLE" > /dev/null 2>&1; then
        aws iam delete-role-policy --role-name "$RDS_IAM_ROLE" --policy-name connect > /dev/null 2>&1 || true
        aws iam delete-role --role-name "$RDS_IAM_ROLE"
    fi
}

cleanup() {
    status=$?
    set +e
    sql "DROP ROLE IF EXISTS tfpg_iam_probe" > /dev/null 2>&1
    sql "DROP ROLE IF EXISTS $DB_USER" > /dev/null 2>&1
    delete_iam
    rm -rf "$WORK"
    exit "$status"
}
trap cleanup EXIT

echo "## $("$TERRAFORM" version | head -n 1)"
echo "## Provider logging in with IAM authentication and an assumed role"

# --- the role of the database: it can only log in with a token
sql "DROP ROLE IF EXISTS tfpg_iam_probe"
sql "DROP ROLE IF EXISTS $DB_USER"
sql "CREATE ROLE $DB_USER LOGIN CREATEROLE"
sql "GRANT rds_iam TO $DB_USER"

# --- the IAM role which is allowed to connect as this role
account=$(aws sts get-caller-identity --query Account --output text)
caller=$(aws sts get-caller-identity --query Arn --output text)
dbi=$(aws rds describe-db-instances --db-instance-identifier "$RDS_NAME" --query 'DBInstances[0].DbiResourceId' --output text)
delete_iam
role_arn=$(aws iam create-role --role-name "$RDS_IAM_ROLE" \
    --description "terraform-provider-postgresql tests (temporary)" --tags Key=purpose,Value=tfpg-test \
    --assume-role-policy-document "{\"Version\": \"2012-10-17\", \"Statement\": [{\"Effect\": \"Allow\", \"Principal\": {\"AWS\": \"arn:aws:iam::${account}:root\"}, \"Action\": \"sts:AssumeRole\"}]}" \
    --query Role.Arn --output text)
aws iam put-role-policy --role-name "$RDS_IAM_ROLE" --policy-name connect \
    --policy-document "{\"Version\": \"2012-10-17\", \"Statement\": [{\"Effect\": \"Allow\", \"Action\": \"rds-db:connect\", \"Resource\": \"arn:aws:rds-db:${REGION}:${account}:dbuser:${dbi}/${DB_USER}\"}]}"
echo "IAM role $RDS_IAM_ROLE created"

# --- the credentials which assume the role
mkdir "$WORK/aws"
(
    umask 077
    if [[ "$caller" == *:root ]]; then
        # the root user of an account cannot assume a role
        aws iam create-user --user-name "$RDS_IAM_USER" --tags Key=purpose,Value=tfpg-test > /dev/null
        aws iam put-user-policy --user-name "$RDS_IAM_USER" --policy-name assume \
            --policy-document "{\"Version\": \"2012-10-17\", \"Statement\": [{\"Effect\": \"Allow\", \"Action\": \"sts:AssumeRole\", \"Resource\": \"${role_arn}\"}]}"
        aws iam create-access-key --user-name "$RDS_IAM_USER" \
            --query 'AccessKey.[AccessKeyId,SecretAccessKey]' --output text > "$WORK/aws/key"
        read -r key secret < "$WORK/aws/key"
        rm "$WORK/aws/key"
        printf 'AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\n' "$key" "$secret" > "$WORK/aws/env"
        echo "IAM user $RDS_IAM_USER created (the current credentials are the ones of the root user)"
    else
        aws configure export-credentials --format env-no-export > "$WORK/aws/env"
    fi
    # The same credentials as profiles, with and without a region
    {
        for profile in tfpg-no-region tfpg-region; do
            echo "[$profile]"
            sed -e 's/^AWS_ACCESS_KEY_ID=/aws_access_key_id = /' -e 's/^AWS_SECRET_ACCESS_KEY=/aws_secret_access_key = /' \
                -e 's/^AWS_SESSION_TOKEN=/aws_session_token = /' -e '/^AWS_CREDENTIAL_EXPIRATION=/d' "$WORK/aws/env"
            echo
        done
    } > "$WORK/aws/credentials"
    printf '[profile tfpg-no-region]\noutput = json\n\n[profile tfpg-region]\nregion = %s\n' "$REGION" > "$WORK/aws/config"
)

# in_aws <env|profile> <command>: run a command with these credentials and NO region in the environment
in_aws() {
    local mode=$1
    shift
    (
        unset AWS_REGION AWS_DEFAULT_REGION AWS_PROFILE AWS_DEFAULT_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
        export AWS_CONFIG_FILE="$WORK/aws/config" AWS_SHARED_CREDENTIALS_FILE="$WORK/aws/credentials" AWS_EC2_METADATA_DISABLED=true
        if [ "$mode" = env ]; then
            set -a
            source "$WORK/aws/env"
            set +a
        fi
        "$@"
    )
}

echo "Waiting until the role can be assumed"
for attempt in $(seq 30); do
    if in_aws env aws sts assume-role --region "$REGION" --role-arn "$role_arn" --role-session-name tfpg-test > /dev/null 2>&1; then
        break
    fi
    if [ "$attempt" = 30 ]; then
        echo "FAIL: the role $RDS_IAM_ROLE cannot be assumed"
        exit 1
    fi
    sleep 5
done

# --- Terraform
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
export TF_IN_AUTOMATION=1
export TF_VAR_pg_host=$PGHOST TF_VAR_pg_user=$DB_USER

# use_provider [<version>]: a new directory with the configuration, for the provider built from
# the sources or for a released version
use_provider() {
    rm -rf "$WORK/tf"
    mkdir "$WORK/tf"
    cp "$HERE/rds-iam/main.tf" "$WORK/tf/"
    local version=""
    if [ -n "${1:-}" ]; then
        version="version = \"$1\""
    fi
    cat > "$WORK/tf/versions.tf" <<TF
terraform {
  required_providers {
    postgresql = {
      source = "$FORK"
      $version
    }
  }
}
TF
    if [ -n "${1:-}" ]; then
        unset TF_CLI_CONFIG_FILE
        "$TERRAFORM" -chdir="$WORK/tf" init -input=false -no-color > "$WORK/init.txt" 2>&1 || { cat "$WORK/init.txt"; exit 1; }
    else
        export TF_CLI_CONFIG_FILE="$WORK/dev.tfrc"
    fi
}

# tf <env|profile> <arguments>
tf() {
    local mode=$1
    shift
    in_aws "$mode" "$TERRAFORM" -chdir="$WORK/tf" "$@"
}

# expect_login <name> <env|profile> <variables>: the provider logs in, creates a role, has an empty
# plan and deletes the role
expect_login() {
    local name=$1 mode=$2 attempt
    shift 2
    # the policy of a new IAM role takes some time to be known by the database
    for attempt in $(seq 12); do
        if tf "$mode" apply -input=false -no-color -auto-approve "$@" > "$WORK/apply.txt" 2>&1; then
            break
        fi
        if [ "$attempt" = 12 ] || ! grep -q "PAM authentication failed" "$WORK/apply.txt"; then
            cat "$WORK/apply.txt"
            echo "FAIL: $name"
            exit 1
        fi
        sleep 10
    done
    if [ "$(sql "SELECT count(*) FROM pg_roles WHERE rolname = 'tfpg_iam_probe'")" != 1 ]; then
        echo "FAIL: $name: the role was not created"
        exit 1
    fi
    if ! tf "$mode" plan -input=false -no-color -detailed-exitcode "$@" > "$WORK/plan.txt" 2>&1; then
        cat "$WORK/plan.txt"
        echo "FAIL: $name: the plan is not empty"
        exit 1
    fi
    tf "$mode" destroy -input=false -no-color -auto-approve "$@" > "$WORK/destroy.txt" 2>&1 || { cat "$WORK/destroy.txt"; exit 1; }
    echo "ok: $name"
}

# expect_error <name> <expected message> <env|profile> <variables>
expect_error() {
    local name=$1 expected=$2 mode=$3
    shift 3
    if tf "$mode" apply -input=false -no-color -auto-approve "$@" > "$WORK/apply.txt" 2>&1; then
        tf "$mode" destroy -input=false -no-color -auto-approve "$@" > /dev/null 2>&1 || true
        echo "FAIL: $name: the apply succeeded"
        exit 1
    fi
    if ! grep -q "$expected" "$WORK/apply.txt"; then
        cat "$WORK/apply.txt"
        echo "FAIL: $name: the error is not \"$expected\""
        exit 1
    fi
    echo "ok: $name"
}

use_provider
expect_login "assumed role, region given by aws_rds_iam_region, no region in the environment" env \
    -var "iam_role_arn=$role_arn" -var "iam_region=$REGION"
expect_login "assumed role, region given by the profile" profile \
    -var "iam_role_arn=$role_arn" -var iam_profile=tfpg-region
expect_login "assumed role, aws_rds_iam_region completes a profile which has no region" profile \
    -var "iam_role_arn=$role_arn" -var iam_profile=tfpg-no-region -var "iam_region=$REGION"
expect_error "without any region the provider says so" "could not find the AWS region" env \
    -var "iam_role_arn=$role_arn"

# The role is known by the database now: a failure of the released version is not a delay of IAM
use_provider "$RELEASED"
expect_error "the released version $RELEASED fails with the same configuration (the bug which is fixed)" \
    "PAM authentication failed" env -var "iam_role_arn=$role_arn" -var "iam_region=$REGION"

echo
echo "All the IAM authentication checks passed"
