#!/bin/bash
# Delete everything created by rds_up.sh. It can be run at any time, even if
# rds_up.sh failed halfway, and it fails if something could not be deleted.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/rds_common.sh"

if aws rds describe-db-instances --db-instance-identifier "$RDS_NAME" > /dev/null 2>&1; then
    echo "Deleting instance $RDS_NAME"
    aws rds delete-db-instance --db-instance-identifier "$RDS_NAME" \
        --skip-final-snapshot --delete-automated-backups > /dev/null 2>&1
    aws rds wait db-instance-deleted --db-instance-identifier "$RDS_NAME"
fi

if aws rds describe-db-parameter-groups --db-parameter-group-name "$RDS_NAME" > /dev/null 2>&1; then
    echo "Deleting parameter group $RDS_NAME"
    aws rds delete-db-parameter-group --db-parameter-group-name "$RDS_NAME"
fi

sg_query=(aws ec2 describe-security-groups --filters "Name=group-name,Values=$RDS_NAME" --query 'SecurityGroups[].GroupId' --output text)
for sg in $("${sg_query[@]}"); do
    echo "Deleting security group $sg"
    aws ec2 delete-security-group --group-id "$sg" > /dev/null
done

if aws secretsmanager describe-secret --secret-id "$RDS_SECRET" > /dev/null 2>&1; then
    echo "Deleting secret $RDS_SECRET"
    aws secretsmanager delete-secret --secret-id "$RDS_SECRET" --force-delete-without-recovery > /dev/null
fi

rm -f "$RDS_STATE"

# Make sure nothing is left
left=0
if aws rds describe-db-instances --db-instance-identifier "$RDS_NAME" > /dev/null 2>&1; then
    echo "Instance $RDS_NAME still exists" >&2
    left=1
fi
if aws rds describe-db-parameter-groups --db-parameter-group-name "$RDS_NAME" > /dev/null 2>&1; then
    echo "Parameter group $RDS_NAME still exists" >&2
    left=1
fi
if [ -n "$("${sg_query[@]}")" ]; then
    echo "Security group $RDS_NAME still exists" >&2
    left=1
fi
if aws secretsmanager describe-secret --secret-id "$RDS_SECRET" --query 'DeletedDate' --output text 2> /dev/null | grep -q None; then
    echo "Secret $RDS_SECRET still exists" >&2
    left=1
fi
[ "$left" -eq 0 ] && echo "Nothing is left of $RDS_NAME"
exit "$left"
