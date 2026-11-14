#!/bin/bash
# Keep the instance created by rds_up.sh reachable from this machine: its security group
# only allows the public IP the machine had when the instance was created, and the
# address of a hosted runner can change during a job (the connections then time out).
#
# It runs until it is killed: every 20 seconds it reads the public IP and allows it in
# the security group when it is a new one. Everything is deleted with the security group.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/rds_common.sh"

allowed=" "
while true; do
    ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com 2> /dev/null)
    if [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && [[ "$allowed" != *" $ip "* ]]; then
        sg=$(aws ec2 describe-security-groups --filters "Name=group-name,Values=$RDS_NAME" \
            --query 'SecurityGroups[0].GroupId' --output text 2> /dev/null)
        if [ -n "$sg" ] && [ "$sg" != None ]; then
            if [ "$allowed" != " " ]; then
                echo "$(date -u +%H:%M:%S) the public IP is now $ip: it is allowed in $sg"
            fi
            # (the first address is already allowed by rds_up.sh: the duplicate is refused)
            aws ec2 authorize-security-group-ingress --group-id "$sg" --protocol tcp --port 5432 --cidr "${ip}/32" > /dev/null 2>&1
            allowed+="$ip "
        fi
    fi
    sleep 20
done
