#!/bin/bash
# Create a temporary Azure Database for PostgreSQL flexible server, reachable only from
# the current public IP, with the authentication of Microsoft Entra, and make the
# current identity of Azure its Entra administrator. It has to be deleted with
# azure_down.sh.
#
#   AZURE_PRINCIPAL_ID    object of Microsoft Entra which becomes the administrator
#   AZURE_PRINCIPAL_NAME  its name: the name of its role in the database
#   AZURE_PRINCIPAL_TYPE  User, Group or ServicePrincipal (a managed identity is one)
# Without them: the user who is logged in with az.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/azure_common.sh"

if [ -z "${AZURE_PRINCIPAL_ID:-}" ]; then
    AZURE_PRINCIPAL_ID=$(az ad signed-in-user show --query id --output tsv)
    AZURE_PRINCIPAL_NAME=$(az ad signed-in-user show --query userPrincipalName --output tsv)
    AZURE_PRINCIPAL_TYPE=User
fi
: "${AZURE_PRINCIPAL_NAME:?AZURE_PRINCIPAL_NAME must be set with AZURE_PRINCIPAL_ID}"
AZURE_PRINCIPAL_TYPE=${AZURE_PRINCIPAL_TYPE:-ServicePrincipal}

ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com)
# Azure wants three kinds of characters in a password
password="Aa1$(openssl rand -hex 16)"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "Creating server $AZURE_NAME (PostgreSQL $PGVERSION)"
cat > "$work/server.json" <<JSON
{
  "location": "$AZURE_LOCATION",
  "tags": {"purpose": "tfpg-test"},
  "sku": {"name": "${AZURE_SKU:-Standard_B1ms}", "tier": "Burstable"},
  "properties": {
    "version": "$PGVERSION",
    "administratorLogin": "$AZURE_USER",
    "administratorLoginPassword": "$password",
    "storage": {"storageSizeGB": 32, "autoGrow": "Disabled"},
    "backup": {"backupRetentionDays": 7, "geoRedundantBackup": "Disabled"},
    "highAvailability": {"mode": "Disabled"},
    "network": {"publicNetworkAccess": "Enabled"},
    "authConfig": {"activeDirectoryAuth": "Enabled", "passwordAuth": "Enabled", "tenantId": "$AZURE_TENANT_ID"}
  }
}
JSON
echo "Waiting for the server to be available"
arm_wait put "" "$work/server.json"

echo "PostgreSQL allowed from $ip only"
echo "{\"properties\": {\"startIpAddress\": \"$ip\", \"endIpAddress\": \"$ip\"}}" > "$work/firewall.json"
arm_wait put "/firewallRules/tests" "$work/firewall.json"

echo "Administrator of Microsoft Entra: $AZURE_PRINCIPAL_TYPE $AZURE_PRINCIPAL_ID"
echo "{\"properties\": {\"principalType\": \"$AZURE_PRINCIPAL_TYPE\", \"principalName\": \"$AZURE_PRINCIPAL_NAME\", \"tenantId\": \"$AZURE_TENANT_ID\"}}" > "$work/admin.json"
arm_wait put "/administrators/$AZURE_PRINCIPAL_ID" "$work/admin.json"

# configure <name> <value>: set a parameter of the server
configure() {
    local name=$1 value=$2 current
    echo "{\"properties\": {\"value\": \"$value\", \"source\": \"user-override\"}}" > "$work/configuration.json"
    arm_wait put "/configurations/$name" "$work/configuration.json"
    current=$(arm get "/configurations/$name" | python3 -c 'import json,sys; print(json.load(sys.stdin)["properties"].get("value", ""))')
    if [ "$current" != "$value" ]; then
        echo "The parameter $name of $AZURE_NAME is not $value after it was set (it is: $current)" >&2
        return 1
    fi
}

# What the acceptance tests need: the extensions they create, pgaudit, and the logical
# replication (publications, subscriptions, replication slots). The last three parameters
# are read when the server starts.
# (max_worker_processes: a small server has 8 background workers and its own extensions use
# most of them. The worker which copies the table of a subscription then never starts: the
# subscription stays in its first state, without any error.)
echo "Parameters of the server"
configure azure.extensions "CITEXT,CUBE,EARTHDISTANCE,PG_TRGM,PGAUDIT,PGCRYPTO,POSTGRES_FDW"
configure shared_preload_libraries "pg_cron,pg_stat_statements,pgaudit"
configure wal_level logical
configure max_worker_processes 16
echo "Restarting the server"
arm_wait post "/restart"
pending=$(arm get "/configurations/wal_level" | python3 -c 'import json,sys; p=json.load(sys.stdin)["properties"]; print(p.get("value"), p.get("isConfigPendingRestart"))')
if [ "$pending" != "logical False" ]; then
    echo "The server $AZURE_NAME did not restart with its parameters (wal_level: $pending)" >&2
    exit 1
fi

host=$(arm get "" | python3 -c 'import json,sys; print(json.load(sys.stdin)["properties"]["fullyQualifiedDomainName"])')

# The replication slots and the subscriptions of the tests (the server subscribes to itself)
# need an administrator which is allowed to replicate
PGPASSWORD=$password PGCONNECT_TIMEOUT=30 psql "host=$host port=5432 user=$AZURE_USER dbname=postgres sslmode=require" \
    -v ON_ERROR_STOP=1 -qAt -c "ALTER ROLE $AZURE_USER REPLICATION"
(
    umask 077
    cat > "$AZURE_STATE" <<STATE
PGHOST=$host
PGPASSWORD=$password
AZURE_PRINCIPAL_NAME=$AZURE_PRINCIPAL_NAME
STATE
)
echo "Server $AZURE_NAME is available: PostgreSQL $(PGPASSWORD=$password PGCONNECT_TIMEOUT=30 psql "host=$host port=5432 user=$AZURE_USER dbname=postgres sslmode=require" -qAt -c 'SHOW server_version')"
