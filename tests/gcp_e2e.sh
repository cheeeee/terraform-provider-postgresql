#!/bin/bash
# Run the end-to-end test of Google Cloud SQL (tests/e2e/gcp.sh) against the instance
# created by gcp_up.sh.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/gcp_common.sh"

if [ ! -f "$GCP_STATE" ]; then
    echo "$GCP_STATE not found, create the instance with gcp_up.sh first" >&2
    exit 1
fi
set -a
source "$GCP_STATE"
set +a

export GCP_PROJECT GCP_NAME GCP_USER
"$HERE/e2e/gcp.sh"
