#!/bin/bash
# Keep the instance created by gcp_up.sh reachable from this machine: it only allows the
# public IP the machine had when the instance was created (and its own address), and the
# address of a hosted runner can change during a job (the connections then time out).
#
# It runs until it is killed: every 20 seconds it reads the public IP and adds it to the
# networks which are allowed when it is a new one.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/gcp_common.sh"

# shellcheck disable=SC1090  # GCP_NETWORKS: what gcp_up.sh allowed
source "$GCP_STATE"
networks=$GCP_NETWORKS
while true; do
    ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com 2> /dev/null)
    if [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && [[ ",$networks," != *",$ip/32,"* ]]; then
        echo "$(date -u +%H:%M:%S) the public IP is now $ip: it is allowed too"
        if gcp sql instances patch "$GCP_NAME" --authorized-networks "$networks,$ip/32" --quiet > /dev/null 2>&1; then
            networks="$networks,$ip/32"
        fi
    fi
    sleep 20
done
