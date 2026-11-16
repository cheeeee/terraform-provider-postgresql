#!/bin/bash
# Create a temporary Google Cloud SQL instance, reachable only from the current public
# IP, with IAM database authentication. It has to be deleted with gcp_down.sh.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/gcp_common.sh"

ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com)
password=$(openssl rand -hex 16)
# IAM database authentication for the end-to-end test; the logical replication (publications,
# subscriptions, replication slots) and pgaudit for the acceptance tests
flags=cloudsql.iam_authentication=on,cloudsql.logical_decoding=on,cloudsql.enable_pgaudit=on

echo "Creating instance $GCP_NAME (PostgreSQL $PGVERSION, PostgreSQL allowed from $ip only)"
# --async: the creation is followed with "describe", which the role of the tests allows
gcp sql instances create "$GCP_NAME" --async --region "$GCP_REGION" \
    --database-version "POSTGRES_${PGVERSION}" --edition ENTERPRISE --tier "${GCP_TIER:-db-custom-1-3840}" \
    --storage-size 10GB --storage-type SSD --no-storage-auto-increase \
    --availability-type zonal --no-backup --no-deletion-protection \
    --root-password "$password" \
    --assign-ip --authorized-networks "${ip}/32" --ssl-mode ENCRYPTED_ONLY \
    --database-flags "$flags" > /dev/null

echo "Waiting for the instance to be available"
for attempt in $(seq 120); do
    state=$(gcp sql instances describe "$GCP_NAME" --format 'value(state)' 2> /dev/null || true)
    if [ "$state" = RUNNABLE ]; then
        break
    fi
    if [ "$attempt" = 120 ]; then
        echo "The instance is not available (state: ${state:-unknown})" >&2
        exit 1
    fi
    sleep 10
done
addresses=$(gcp sql instances describe "$GCP_NAME" --format json | python3 -c '
import json, sys
by_type = {}
for address in json.load(sys.stdin)["ipAddresses"]:
    by_type.setdefault(address["type"], []).append(address["ipAddress"])
print(by_type["PRIMARY"][0], *by_type.get("OUTGOING", []))')
host=${addresses%% *}

# The subscription tests make the instance subscribe to itself. Cloud SQL refuses the
# loopback address: the instance connects to its public address, from the address its own
# connections go out with, which has to be allowed like the one of this machine. And the
# user of the subscription needs the right to replicate.
networks="${ip}/32"
for outgoing in ${addresses#* }; do
    networks="${networks},${outgoing}/32"
done
echo "PostgreSQL allowed from the instance itself too ($networks)"
gcp sql instances patch "$GCP_NAME" --authorized-networks "$networks" --quiet > /dev/null 2>&1
PGPASSWORD=$password PGCONNECT_TIMEOUT=30 psql "host=$host port=5432 user=$GCP_USER dbname=postgres sslmode=require" \
    -v ON_ERROR_STOP=1 -qAt -c "ALTER ROLE $GCP_USER REPLICATION"

(
    umask 077
    cat > "$GCP_STATE" <<STATE
PGHOST=$host
PGPASSWORD=$password
GCP_NETWORKS=$networks
STATE
)
echo "Instance $GCP_NAME is available: PostgreSQL $(PGPASSWORD=$password PGCONNECT_TIMEOUT=30 psql "host=$host port=5432 user=$GCP_USER dbname=postgres sslmode=require" -qAt -c 'SHOW server_version')"
