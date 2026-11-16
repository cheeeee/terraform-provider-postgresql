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

# arm_wait <method> <path under the server, or ""> [<file with the body>]: send a request which
# starts an operation of Azure, and wait for the end of the operation.
#
# The answer to such a request only says that it was accepted. A request which is sent while
# an operation of the server still runs is accepted too, and dropped: every operation is
# followed to its end before the next request is sent.
arm_wait() {
    local method=$1 path=$2 body=${3:-} headers token operation answer status
    headers=$(mktemp)
    token=$(az account get-access-token --resource https://management.azure.com --query accessToken --output tsv)
    if ! curl --fail-with-body -sS -X "$(tr '[:lower:]' '[:upper:]' <<< "$method")" -D "$headers" -o "$headers.body" \
        -H "Authorization: Bearer $token" -H "Content-Type: application/json" --data "${body:+@$body}" \
        "${AZURE_SERVER_URL}${path}?api-version=${AZURE_API_VERSION}"; then
        cat "$headers.body" >&2
        rm -f "$headers" "$headers.body"
        return 1
    fi
    operation=$(awk 'tolower($1) == "azure-asyncoperation:" {print $2}' "$headers" | tr -d '\r')
    rm -f "$headers" "$headers.body"
    if [ -z "$operation" ]; then
        return 0
    fi
    # (a server is created in 5 to 10 minutes, the other operations last some seconds)
    for _ in $(seq 360); do
        answer=$(curl --fail-with-body -sS -H "Authorization: Bearer $token" "$operation") || {
            echo "The operation of $AZURE_NAME$path could not be read: $answer" >&2
            return 1
        }
        status=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("status", ""))' <<< "$answer")
        case "$status" in
        Succeeded)
            return 0
            ;;
        InProgress | Accepted | Running) ;;
        *)
            echo "The operation of $AZURE_NAME$path ended with: $answer" >&2
            return 1
            ;;
        esac
        sleep 5
    done
    echo "The operation of $AZURE_NAME$path did not end in 30 minutes" >&2
    return 1
}
