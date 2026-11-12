#!/bin/bash
# Run the Terraform end-to-end test (tests/e2e) against the local test database.
set -e

cd "$(dirname "${BASH_SOURCE[0]}")/.."

./tests/testacc_setup.sh
source ./tests/switch_superuser.sh

# Use psql from the postgres container if it's not installed locally
if ! command -v psql > /dev/null; then
    export E2E_PSQL="docker compose -f $(pwd)/tests/docker-compose.yml exec -T postgres psql -U postgres"
fi

status=0
./tests/e2e/run.sh || status=$?

./tests/testacc_cleanup.sh
exit $status
