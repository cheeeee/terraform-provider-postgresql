#!/bin/bash
# Run the Terraform end-to-end test (tests/e2e) against the local test database.
set -e

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The end-to-end tests give the provider which was just built to the CLI with dev_overrides
# and run it with -chdir: Terraform has both since 0.14. They are skipped, and it is said,
# with an older Terraform: the acceptance tests are what is run with it.
cli_version=$("${E2E_TERRAFORM:-terraform}" version | head -1)
case "$cli_version" in
    "Terraform v0.12."* | "Terraform v0.13."*)
        reason="the end-to-end tests are skipped with $cli_version: they need dev_overrides and -chdir, which Terraform has since 0.14"
        echo "SKIP: $reason"
        if [ -n "${GITHUB_ACTIONS:-}" ]; then
            echo "::notice title=End-to-end tests::$reason"
        fi
        exit 0
        ;;
esac

./tests/testacc_setup.sh
source ./tests/switch_superuser.sh

# Use psql from the postgres container if it's not installed locally
if ! command -v psql > /dev/null; then
    E2E_PSQL="docker compose -f $(pwd)/tests/docker-compose.yml exec -T postgres psql -U postgres"
    export E2E_PSQL
fi

status=0
./tests/e2e/run.sh || status=$?
# Upgrade from the released versions of the provider
./tests/e2e/upgrade.sh || status=$?

./tests/testacc_cleanup.sh
exit $status
