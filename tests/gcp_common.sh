#!/bin/bash
# Settings shared by the gcp_*.sh scripts, which test the provider against a temporary
# Google Cloud SQL instance (see .github/workflows/gcp-test.yml).

: "${PGVERSION:?PGVERSION must be set to a PostgreSQL major version (e.g.: 17)}"
: "${GCP_PROJECT:?GCP_PROJECT must be set to the project of the tests}"

export GCP_REGION=${GCP_REGION:-northamerica-northeast1}
export CLOUDSDK_CORE_DISABLE_PROMPTS=1

# Name of the instance. GCP_SUFFIX allows several instances of the same version at the
# same time. (A name of Cloud SQL cannot be used again for a week after a deletion: the
# suffix of a run is unique.)
GCP_NAME=${GCP_NAME:-tfpg-test}-pg${PGVERSION}${GCP_SUFFIX:+-$GCP_SUFFIX}
# The name the provider and the Cloud SQL proxy know the instance by
export GCP_CONNECTION_NAME="${GCP_PROJECT}:${GCP_REGION}:${GCP_NAME}"
# File where the connection settings of the instance are stored.
GCP_STATE=${GCP_STATE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/${GCP_NAME}.env}
export GCP_USER=postgres

gcp() {
    gcloud --project "$GCP_PROJECT" "$@"
}
