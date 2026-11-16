#!/bin/bash
# Keep the server created by azure_up.sh reachable from this machine: its firewall only
# allows the public IP the machine had when the server was created, and the address of a
# hosted runner can change during a job (the connections then time out).
#
# It runs until it is killed: every 20 seconds it reads the public IP and adds a rule to
# the firewall when it is a new one. The rules are deleted with the server.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/azure_common.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# (the first address is the one azure_up.sh allowed)
allowed=" $(curl -fsS --max-time 10 https://checkip.amazonaws.com 2> /dev/null) "
count=0
while true; do
    ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com 2> /dev/null)
    if [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && [[ "$allowed" != *" $ip "* ]]; then
        count=$((count + 1))
        echo "$(date -u +%H:%M:%S) the public IP is now $ip: it is allowed too"
        echo "{\"properties\": {\"startIpAddress\": \"$ip\", \"endIpAddress\": \"$ip\"}}" > "$work/firewall.json"
        if arm_wait put "/firewallRules/tests-$count" "$work/firewall.json"; then
            allowed+="$ip "
        fi
    fi
    sleep 20
done
