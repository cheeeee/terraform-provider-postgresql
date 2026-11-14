#!/bin/bash
# Settings shared by the azure_*.sh scripts, which test the provider against a temporary
# Azure Database for PostgreSQL flexible server (see .github/workflows/azure-test.yml).
#
# The server is managed with the REST API of Azure (az rest): the commands of
# "az postgres flexible-server" also read the subscription, which the identity of the
# tests is not allowed to do (tests/azure/setup.sh).

: "${PGVERSION:?PGVERSION must be set to a PostgreSQL major version (e.g.: 17)}"

AZURE_RESOURCE_GROUP=${AZURE_RESOURCE_GROUP:-tfpg-test}
AZURE_LOCATION=${AZURE_LOCATION:-canadacentral}
AZURE_API_VERSION=${AZURE_API_VERSION:-2024-08-01}

# Name of the server (it is a DNS name: unique in Azure, hence the suffix of the run).
# AZURE_SUFFIX allows several servers of the same version at the same time.
AZURE_NAME=${AZURE_NAME:-tfpg-test}-pg${PGVERSION}${AZURE_SUFFIX:+-$AZURE_SUFFIX}
# File where the connection settings of the server are stored.
AZURE_STATE=${AZURE_STATE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/${AZURE_NAME}.env}
export AZURE_USER=tfadmin

AZURE_SUBSCRIPTION_ID=${AZURE_SUBSCRIPTION_ID:-$(az account show --query id --output tsv)}
AZURE_TENANT_ID=${AZURE_TENANT_ID:-$(az account show --query tenantId --output tsv)}
AZURE_SERVERS_URL="https://management.azure.com/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${AZURE_RESOURCE_GROUP}/providers/Microsoft.DBforPostgreSQL/flexibleServers"
AZURE_SERVER_URL="${AZURE_SERVERS_URL}/${AZURE_NAME}"

# arm <method> <path under the server, or ""> [<file with the body>]
arm() {
    local method=$1 path=$2 body=${3:-}
    if [ -n "$body" ]; then
        az rest --method "$method" --url "${AZURE_SERVER_URL}${path}?api-version=${AZURE_API_VERSION}" --body "@$body" --output json
    else
        az rest --method "$method" --url "${AZURE_SERVER_URL}${path}?api-version=${AZURE_API_VERSION}" --output json
    fi
}
