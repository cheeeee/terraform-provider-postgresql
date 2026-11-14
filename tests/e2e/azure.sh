#!/bin/bash
# End-to-end test which only makes sense on Azure: the provider logs in to an Azure
# Database for PostgreSQL flexible server with a token of Microsoft Entra
# (azure_identity_auth), and the server checks the token. Nothing else tests it against
# a server of Azure.
#
# It needs:
#   PGHOST, PGPASSWORD      name of the server and password of AZURE_USER
#   AZURE_PRINCIPAL_NAME    role of the database of the identity the provider logs in with:
#                           the Entra administrator of the server (azure_up.sh)
#   AZURE_TENANT_ID         tenant of Microsoft Entra
# The identity is the one of the environment: the user of "az login", or, in GitHub
# Actions, the identity of AZURE_CLIENT_ID with the token of the job (a federated
# credential: the token of GitHub only lives a few minutes, a new one is asked before
# every command).
# Everything it creates is deleted at the end (and with the server).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

: "${PGHOST:?PGHOST must be set}"
: "${PGPASSWORD:?PGPASSWORD must be set}"
: "${AZURE_PRINCIPAL_NAME:?AZURE_PRINCIPAL_NAME must be set}"
: "${AZURE_TENANT_ID:?AZURE_TENANT_ID must be set}"
PGUSER=${AZURE_USER:-tfadmin}
TERRAFORM=${E2E_TERRAFORM:-terraform}
FORK=registry.terraform.io/cheeeee/postgresql

WORK=$(mktemp -d)

# E2E_PSQL can be set to another command to run psql (see e2e_local.sh)
sql() {
    if [ -n "${E2E_PSQL:-}" ]; then
        $E2E_PSQL -d postgres -v ON_ERROR_STOP=1 -qAt -c "$1"
    else
        PGPASSWORD=$PGPASSWORD psql "host=$PGHOST port=5432 user=$PGUSER dbname=postgres sslmode=require" -v ON_ERROR_STOP=1 -qAt -c "$1"
    fi
}

# In GitHub Actions: the token of the job, for the federated credential of the identity
github_token() {
    if [ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ]; then
        return
    fi
    : "${AZURE_CLIENT_ID:?AZURE_CLIENT_ID must be set}"
    curl -fsS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
        "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=api://AzureADTokenExchange" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["value"], end="")' > "$WORK/github-token"
    export AZURE_FEDERATED_TOKEN_FILE="$WORK/github-token" AZURE_CLIENT_ID AZURE_TENANT_ID
}

tf() {
    github_token
    "$TERRAFORM" -chdir="$WORK/tf" "$@"
}

cleanup() {
    status=$?
    set +e
    if [ -d "$WORK/tf" ]; then
        tf destroy -input=false -auto-approve -var "pg_host=$PGHOST" -var "pg_user=$AZURE_PRINCIPAL_NAME" -var "tenant_id=$AZURE_TENANT_ID" > /dev/null 2>&1
    fi
    sql "DROP ROLE IF EXISTS tfpg_azure_probe" > /dev/null 2>&1
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
# (PGPASSWORD is unset around every command of Terraform: the password of the
# administrator must not help the provider)

mkdir "$WORK/tf"
cp "$HERE/azure/main.tf" "$WORK/tf/"
cat > "$WORK/tf/versions.tf" <<TF
terraform {
  required_providers {
    postgresql = {
      source = "$FORK"
    }
  }
}
TF

echo "## Provider logging in with a token of Microsoft Entra"
sql "DROP ROLE IF EXISTS tfpg_azure_probe"
variables=(-var "pg_host=$PGHOST" -var "tenant_id=$AZURE_TENANT_ID")

# the administrator which was just declared takes some time to be known by the server
for attempt in $(seq 12); do
    if (unset PGPASSWORD; tf apply -input=false -no-color -auto-approve "${variables[@]}" -var "pg_user=$AZURE_PRINCIPAL_NAME" > "$WORK/apply.txt" 2>&1); then
        break
    fi
    if [ "$attempt" = 12 ] || ! grep -qiE "authentication failed|does not exist|token" "$WORK/apply.txt"; then
        cat "$WORK/apply.txt"
        echo "FAIL: the provider does not log in with a token"
        exit 1
    fi
    sleep 10
done
if [ "$(sql "SELECT count(*) FROM pg_roles WHERE rolname = 'tfpg_azure_probe'")" != 1 ]; then
    echo "FAIL: the role was not created"
    exit 1
fi
echo "ok: the provider logs in as $AZURE_PRINCIPAL_NAME with a token, and creates a role"

if ! (unset PGPASSWORD; tf plan -input=false -no-color -detailed-exitcode "${variables[@]}" -var "pg_user=$AZURE_PRINCIPAL_NAME" > "$WORK/plan.txt" 2>&1); then
    cat "$WORK/plan.txt"
    echo "FAIL: the plan is not empty"
    exit 1
fi
echo "ok: the plan is empty"

# The token is checked by the server: it is not the password of the administrator with a password
if (unset PGPASSWORD; tf plan -input=false -no-color "${variables[@]}" -var "pg_user=$PGUSER" > "$WORK/plan.txt" 2>&1); then
    echo "FAIL: the token is accepted for $PGUSER, which is not an identity of Microsoft Entra"
    exit 1
fi
if ! grep -qiE "authentication failed" "$WORK/plan.txt"; then
    cat "$WORK/plan.txt"
    echo "FAIL: the error is not an authentication failure"
    exit 1
fi
echo "ok: the token is refused for a role which is not an identity of Microsoft Entra"

(unset PGPASSWORD; tf destroy -input=false -no-color -auto-approve "${variables[@]}" -var "pg_user=$AZURE_PRINCIPAL_NAME" > "$WORK/destroy.txt" 2>&1) || { cat "$WORK/destroy.txt"; exit 1; }
if [ "$(sql "SELECT count(*) FROM pg_roles WHERE rolname = 'tfpg_azure_probe'")" != 0 ]; then
    echo "FAIL: the role was not deleted"
    exit 1
fi
echo "ok: the role is deleted"

echo
echo "All the Azure checks passed"
