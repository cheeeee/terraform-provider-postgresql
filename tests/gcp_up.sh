#!/bin/bash
# Create a temporary Google Cloud SQL instance, reachable only from the current public
# IP, with IAM database authentication. It has to be deleted with gcp_down.sh.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/gcp_common.sh"

ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com)
password=$(openssl rand -hex 16)

echo "Creating instance $GCP_NAME (PostgreSQL $PGVERSION, PostgreSQL allowed from $ip only)"
# --async: the creation is followed with "describe", which the role of the tests allows
gcp sql instances create "$GCP_NAME" --async --region "$GCP_REGION" \
    --database-version "POSTGRES_${PGVERSION}" --edition ENTERPRISE --tier "${GCP_TIER:-db-custom-1-3840}" \
    --storage-size 10GB --storage-type SSD --no-storage-auto-increase \
    --availability-type zonal --no-backup --no-deletion-protection \
    --root-password "$password" \
    --assign-ip --authorized-networks "${ip}/32" --ssl-mode ENCRYPTED_ONLY \
    --database-flags cloudsql.iam_authentication=on > /dev/null

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
host=$(gcp sql instances describe "$GCP_NAME" --format 'value(ipAddresses[0].ipAddress)')

(
    umask 077
    cat > "$GCP_STATE" <<STATE
PGHOST=$host
PGPASSWORD=$password
STATE
)
echo "Instance $GCP_NAME is available"
