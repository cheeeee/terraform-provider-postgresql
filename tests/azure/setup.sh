#!/bin/bash
# What GitHub Actions needs to run the azure-test workflow in an Azure subscription, and
# nothing more. It is run once, by an owner of the subscription; it can be run again.
#
#   tests/azure/setup.sh <subject prefix> [<location> [<environment>]]
#
# <subject prefix> is how GitHub names the repository in its tokens:
#   gh api repos/<owner>/<repository>/actions/oidc/customization/sub -q .sub_claim_prefix
#
# It creates, in the subscription az is logged in to:
#   - the resource group tfpg-test: everything the tests create lives in it;
#   - the managed identity tfpg-test-github, which only the environment of this repository
#     can use (a federated credential: OpenID Connect, no secret);
#   - a role of its own for this identity, in this resource group only: create, configure
#     and delete PostgreSQL flexible servers. It has no right on the subscription, on the
#     other resource groups or on Microsoft Entra.
# Nothing here costs money while no test runs. Deleting the resource group and the role
# removes everything.
set -euo pipefail

SUBJECT_PREFIX=${1:?subject prefix of the repository (repo:owner@id/repository@id)}
LOCATION=${2:-canadacentral}
ENVIRONMENT=${3:-azure-test}

GROUP=tfpg-test
IDENTITY=tfpg-test-github
ROLE="terraform-provider-postgresql tests"

subject="${SUBJECT_PREFIX}:environment:${ENVIRONMENT}"
subscription=$(az account show --query id --output tsv)
tenant=$(az account show --query tenantId --output tsv)
scope="/subscriptions/${subscription}/resourceGroups/${GROUP}"

echo "## Resource providers"
for provider in Microsoft.DBforPostgreSQL Microsoft.ManagedIdentity; do
    az provider register --namespace "$provider" --wait
done

echo "## Resource group $GROUP ($LOCATION)"
az group create --name "$GROUP" --location "$LOCATION" --tags purpose=tfpg-test-infra --output none

echo "## Managed identity $IDENTITY: only $subject"
az identity create --resource-group "$GROUP" --name "$IDENTITY" --output none
az identity federated-credential create --resource-group "$GROUP" --identity-name "$IDENTITY" --name github \
    --issuer https://token.actions.githubusercontent.com --subject "$subject" \
    --audiences api://AzureADTokenExchange --output none
client=$(az identity show --resource-group "$GROUP" --name "$IDENTITY" --query clientId --output tsv)
principal=$(az identity show --resource-group "$GROUP" --name "$IDENTITY" --query principalId --output tsv)

echo "## Role of the identity, in the resource group only"
definition=$(mktemp)
cat > "$definition" <<JSON
{
  "Name": "$ROLE",
  "Description": "Create, configure and delete the PostgreSQL flexible servers of the tests",
  "Actions": [
    "Microsoft.Resources/subscriptions/resourceGroups/read",
    "Microsoft.DBforPostgreSQL/flexibleServers/*"
  ],
  "NotActions": [],
  "AssignableScopes": ["$scope"]
}
JSON
if [ -n "$(az role definition list --name "$ROLE" --scope "$scope" --query '[0].name' --output tsv)" ]; then
    az role definition update --role-definition "$definition" --output none
else
    az role definition create --role-definition "$definition" --output none
fi
rm -f "$definition"

# A role and an identity which have just been created are not known everywhere at once
for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if az role assignment create --assignee-object-id "$principal" --assignee-principal-type ServicePrincipal \
        --role "$ROLE" --scope "$scope" --output none 2> /dev/null; then
        break
    fi
    if [ "$attempt" = 10 ]; then
        echo "The role could not be assigned" >&2
        exit 1
    fi
    sleep 15
done

echo
echo "Variables of the environment $ENVIRONMENT of the repository:"
echo "  AZURE_CLIENT_ID=$client"
echo "  AZURE_TENANT_ID=$tenant"
echo "  AZURE_SUBSCRIPTION_ID=$subscription"
echo "  AZURE_RESOURCE_GROUP=$GROUP"
echo "  AZURE_LOCATION=$LOCATION"
echo "  AZURE_PRINCIPAL_ID=$principal"
