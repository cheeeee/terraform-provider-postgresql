#!/bin/bash
# What GitHub Actions needs to run the gcp-test workflow in a Google Cloud project, and
# nothing more. It is run once, by an owner of the project; it can be run again.
#
#   tests/gcp/setup.sh <project> <subject prefix> [<environment>]
#
# <subject prefix> is how GitHub names the repository in its tokens:
#   gh api repos/<owner>/<repository>/actions/oidc/customization/sub -q .sub_claim_prefix
#
# It creates:
#   - a workload identity pool which only trusts the tokens of the environment of this
#     repository (OpenID Connect, no stored key);
#   - the service account of the workflow, tfpg-test-github, with a role of its own: it
#     creates, reads, connects to and deletes the Cloud SQL instances named tfpg-test-*,
#     and manages the users of the instances of the project (Google Cloud cannot limit
#     this to some instances); it has no right on IAM;
#   - the service account the provider impersonates in the tests, tfpg-test-connect, which
#     can only connect and log in to the instances.
# Nothing here costs money while no test runs. To remove everything: delete the two
# service accounts, the two roles (tfpgTest, tfpgTestUsers) and the pool "github".
set -euo pipefail

PROJECT=${1:?project}
SUBJECT_PREFIX=${2:?subject prefix of the repository (repo:owner@id/repository@id)}
ENVIRONMENT=${3:-gcp-test}

POOL=github
PROVIDER=github-actions
ROLE=tfpgTest
USERS_ROLE=tfpgTestUsers
WORKFLOW_SA=tfpg-test-github
CONNECT_SA=tfpg-test-connect
PREFIX=tfpg-test-

subject="${SUBJECT_PREFIX}:environment:${ENVIRONMENT}"
number=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
workflow_email="${WORKFLOW_SA}@${PROJECT}.iam.gserviceaccount.com"
connect_email="${CONNECT_SA}@${PROJECT}.iam.gserviceaccount.com"

g() {
    gcloud --project "$PROJECT" --quiet "$@"
}

echo "## APIs"
g services enable sqladmin.googleapis.com iam.googleapis.com iamcredentials.googleapis.com \
    sts.googleapis.com cloudresourcemanager.googleapis.com

echo "## Workload identity pool: only $subject"
if ! g iam workload-identity-pools describe "$POOL" --location=global > /dev/null 2>&1; then
    g iam workload-identity-pools create "$POOL" --location=global \
        --display-name="GitHub Actions" --description="terraform-provider-postgresql tests"
fi
provider_settings=(--location=global --workload-identity-pool="$POOL"
    --attribute-mapping="google.subject=assertion.sub"
    --attribute-condition="assertion.sub == '${subject}'")
if g iam workload-identity-pools providers describe "$PROVIDER" --location=global --workload-identity-pool="$POOL" > /dev/null 2>&1; then
    g iam workload-identity-pools providers update-oidc "$PROVIDER" "${provider_settings[@]}"
else
    g iam workload-identity-pools providers create-oidc "$PROVIDER" "${provider_settings[@]}" \
        --issuer-uri="https://token.actions.githubusercontent.com" --display-name="GitHub Actions"
fi

echo "## Service accounts"
for account in "$WORKFLOW_SA:Workflow of the tests (GitHub Actions)" "$CONNECT_SA:Impersonated by the provider in the tests"; do
    name=${account%%:*}
    if ! g iam service-accounts describe "${name}@${PROJECT}.iam.gserviceaccount.com" > /dev/null 2>&1; then
        g iam service-accounts create "$name" --display-name="${account#*:}"
    fi
done

echo "## Roles of the workflow"
# role <name> <description> <permissions>
role() {
    if g iam roles describe "$1" > /dev/null 2>&1; then
        g iam roles update "$1" --permissions="$3" --stage=GA > /dev/null
    else
        g iam roles create "$1" --title="terraform-provider-postgresql tests" --stage=GA \
            --description="$2" --permissions="$3" > /dev/null
    fi
}
role "$ROLE" "Create, connect to and delete the Cloud SQL instances of the tests" \
    cloudsql.instances.create,cloudsql.instances.get,cloudsql.instances.delete,cloudsql.instances.connect
role "$USERS_ROLE" "Manage the users of the Cloud SQL instances of the tests" \
    cloudsql.users.create,cloudsql.users.list,cloudsql.users.delete

echo "## Bindings"
# A service account which has just been created is not known everywhere at once
retry() {
    local attempt
    for attempt in 1 2 3 4 5 6; do
        if "$@"; then
            return 0
        fi
        sleep 10
    done
    return 1
}
# The workflow: its role, on the instances of the tests only
retry g projects add-iam-policy-binding "$PROJECT" --member="serviceAccount:${workflow_email}" \
    --role="projects/${PROJECT}/roles/${ROLE}" \
    --condition="title=tfpg-test instances,expression=resource.name.startsWith(\"projects/${PROJECT}/instances/${PREFIX}\")" > /dev/null
# ... and the users of the instances: a condition on the name of the instance is not
# applied to them, they are allowed on the project
retry g projects add-iam-policy-binding "$PROJECT" --member="serviceAccount:${workflow_email}" \
    --role="projects/${PROJECT}/roles/${USERS_ROLE}" --condition=None > /dev/null
# The impersonated account: connect and log in, on the instances of the tests only
for role in roles/cloudsql.client roles/cloudsql.instanceUser; do
    retry g projects add-iam-policy-binding "$PROJECT" --member="serviceAccount:${connect_email}" --role="$role" \
        --condition="title=tfpg-test instances,expression=resource.name.startsWith(\"projects/${PROJECT}/instances/${PREFIX}\")" > /dev/null
done
# The workflow impersonates this account, and only this one
retry g iam service-accounts add-iam-policy-binding "$connect_email" --member="serviceAccount:${workflow_email}" \
    --role=roles/iam.serviceAccountTokenCreator > /dev/null
# The tokens of GitHub become the service account of the workflow
retry g iam service-accounts add-iam-policy-binding "$workflow_email" --role=roles/iam.workloadIdentityUser \
    --member="principal://iam.googleapis.com/projects/${number}/locations/global/workloadIdentityPools/${POOL}/subject/${subject}" > /dev/null

echo
echo "Variables of the environment $ENVIRONMENT of the repository:"
echo "  GCP_PROJECT=$PROJECT"
echo "  GCP_WORKLOAD_IDENTITY_PROVIDER=projects/${number}/locations/global/workloadIdentityPools/${POOL}/providers/${PROVIDER}"
echo "  GCP_SERVICE_ACCOUNT=$workflow_email"
echo "  GCP_CONNECT_SERVICE_ACCOUNT=$connect_email"
