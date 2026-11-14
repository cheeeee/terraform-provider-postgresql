#!/bin/bash
# Run the end-to-end test of Azure (tests/e2e/azure.sh) against the server created by
# azure_up.sh.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/azure_common.sh"

if [ ! -f "$AZURE_STATE" ]; then
    echo "$AZURE_STATE not found, create the server with azure_up.sh first" >&2
    exit 1
fi
set -a
source "$AZURE_STATE"
set +a

export AZURE_TENANT_ID AZURE_USER
"$HERE/e2e/azure.sh"
