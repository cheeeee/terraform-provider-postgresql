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

# wait_for <path> [<attempts>]: until the resource is there (the answer of a PUT is an
# operation which goes on; when it fails, the resource never comes)
wait_for() {
    local path=$1 attempts=${2:-120} state
    for _ in $(seq "$attempts"); do
        state=$(arm get "$path" 2> /dev/null | python3 -c 'import json,sys; p=json.load(sys.stdin).get("properties",{}); print(p.get("state") or p.get("provisioningState") or "there")' 2> /dev/null || true)
        case "$state" in
        Ready | Succeeded | there)
            return 0
            ;;
        esac
        sleep 10
    done
    echo "$AZURE_NAME$path is not available (state: ${state:-unknown}): see the activity log of the resource group" >&2
    return 1
}

# ensure <path> <file with the body>: create a resource of the server. The server sometimes
# accepts the request and drops it (it is busy just after its creation): it is sent again.
ensure() {
    local path=$1 body=$2
    for _ in 1 2 3 4; do
        arm put "$path" "$body" > /dev/null || true
        if wait_for "$path" 18 2> /dev/null; then
            return 0
        fi
    done
    echo "$AZURE_NAME$path could not be created: see the activity log of the resource group" >&2
    return 1
}

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
arm put "" "$work/server.json" > /dev/null
echo "Waiting for the server to be available"
wait_for ""

echo "PostgreSQL allowed from $ip only"
echo "{\"properties\": {\"startIpAddress\": \"$ip\", \"endIpAddress\": \"$ip\"}}" > "$work/firewall.json"
ensure "/firewallRules/tests" "$work/firewall.json"

echo "Administrator of Microsoft Entra: $AZURE_PRINCIPAL_TYPE $AZURE_PRINCIPAL_ID"
echo "{\"properties\": {\"principalType\": \"$AZURE_PRINCIPAL_TYPE\", \"principalName\": \"$AZURE_PRINCIPAL_NAME\", \"tenantId\": \"$AZURE_TENANT_ID\"}}" > "$work/admin.json"
ensure "/administrators/$AZURE_PRINCIPAL_ID" "$work/admin.json"

host=$(arm get "" | python3 -c 'import json,sys; print(json.load(sys.stdin)["properties"]["fullyQualifiedDomainName"])')
(
    umask 077
    cat > "$AZURE_STATE" <<STATE
PGHOST=$host
PGPASSWORD=$password
AZURE_PRINCIPAL_NAME=$AZURE_PRINCIPAL_NAME
STATE
)
echo "Server $AZURE_NAME is available"
