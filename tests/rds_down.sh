#!/bin/bash
# Delete everything created by rds_up.sh. It can be run at any time, even if
# rds_up.sh failed halfway, and it fails if something could not be deleted.
#
# A resource is only taken for absent when AWS says it does not exist: a call which
# fails for another reason (a refused call, a crash of the CLI) is tried again, and it
# makes the script fail if it keeps failing. What is left is deleted by rds_janitor.sh.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/rds_common.sh"

unknown=0

# exists <command which describes a resource>: 0 when it exists, 1 when AWS says it does
# not, 2 when it could not be known
exists() {
    local output
    for _ in 1 2 3; do
        if output=$("$@" 2>&1); then
            return 0
        fi
        if echo "$output" | grep -qE "NotFound|NoSuchEntity|ResourceNotFoundException"; then
            return 1
        fi
        sleep 5
    done
    echo "Could not check ($*): $output" >&2
    unknown=1
    return 2
}

# security_groups: the identifiers of the security groups of the instance; it fails when
# they could not be listed
security_groups() {
    for _ in 1 2 3; do
        if aws ec2 describe-security-groups --filters "Name=group-name,Values=$RDS_NAME" \
            --query 'SecurityGroups[].GroupId' --output text; then
            return 0
        fi
        sleep 5
    done
    echo "Could not list the security groups named $RDS_NAME" >&2
    return 1
}

if exists aws rds describe-db-instances --db-instance-identifier "$RDS_NAME"; then
    echo "Deleting instance $RDS_NAME"
    aws rds delete-db-instance --db-instance-identifier "$RDS_NAME" \
        --skip-final-snapshot --delete-automated-backups > /dev/null 2>&1
    aws rds wait db-instance-deleted --db-instance-identifier "$RDS_NAME"
fi

if exists aws rds describe-db-parameter-groups --db-parameter-group-name "$RDS_NAME"; then
    echo "Deleting parameter group $RDS_NAME"
    aws rds delete-db-parameter-group --db-parameter-group-name "$RDS_NAME"
fi

# (the function runs in a subshell here: its failure is recorded by the caller)
groups=$(security_groups) || unknown=1
for sg in $groups; do
    echo "Deleting security group $sg"
    aws ec2 delete-security-group --group-id "$sg" > /dev/null
done

if exists aws secretsmanager describe-secret --secret-id "$RDS_SECRET"; then
    echo "Deleting secret $RDS_SECRET"
    aws secretsmanager delete-secret --secret-id "$RDS_SECRET" --force-delete-without-recovery > /dev/null
fi

# The IAM role and the IAM user of the end-to-end test: nothing is created in IAM when
# the test is given a role which exists (the credentials then have no right on IAM)
if [ -z "${RDS_IAM_EXISTING_ROLE:-}" ]; then
    if exists aws iam get-user --user-name "$RDS_IAM_USER"; then
        echo "Deleting IAM user $RDS_IAM_USER"
        for key in $(aws iam list-access-keys --user-name "$RDS_IAM_USER" --query 'AccessKeyMetadata[].AccessKeyId' --output text); do
            aws iam delete-access-key --user-name "$RDS_IAM_USER" --access-key-id "$key"
        done
        aws iam delete-user-policy --user-name "$RDS_IAM_USER" --policy-name assume > /dev/null 2>&1
        aws iam delete-user --user-name "$RDS_IAM_USER"
    fi

    if exists aws iam get-role --role-name "$RDS_IAM_ROLE"; then
        echo "Deleting IAM role $RDS_IAM_ROLE"
        aws iam delete-role-policy --role-name "$RDS_IAM_ROLE" --policy-name connect > /dev/null 2>&1
        aws iam delete-role --role-name "$RDS_IAM_ROLE"
    fi
fi

rm -f "$RDS_STATE"

# Make sure nothing is left
left=0
if exists aws rds describe-db-instances --db-instance-identifier "$RDS_NAME"; then
    echo "Instance $RDS_NAME still exists" >&2
    left=1
fi
if exists aws rds describe-db-parameter-groups --db-parameter-group-name "$RDS_NAME"; then
    echo "Parameter group $RDS_NAME still exists" >&2
    left=1
fi
groups=$(security_groups) || unknown=1
if [ -n "$groups" ]; then
    echo "Security group $RDS_NAME still exists" >&2
    left=1
fi
# A secret which is deleted without recovery can still be described for a moment
if exists aws secretsmanager describe-secret --secret-id "$RDS_SECRET" &&
    aws secretsmanager describe-secret --secret-id "$RDS_SECRET" --query 'DeletedDate' --output text 2> /dev/null | grep -q None; then
    echo "Secret $RDS_SECRET still exists" >&2
    left=1
fi
if [ -z "${RDS_IAM_EXISTING_ROLE:-}" ]; then
    if exists aws iam get-user --user-name "$RDS_IAM_USER"; then
        echo "IAM user $RDS_IAM_USER still exists" >&2
        left=1
    fi
    if exists aws iam get-role --role-name "$RDS_IAM_ROLE"; then
        echo "IAM role $RDS_IAM_ROLE still exists" >&2
        left=1
    fi
fi

if [ "$unknown" -ne 0 ]; then
    echo "Something could not be checked: $RDS_NAME may not be deleted entirely" >&2
    exit 1
fi
[ "$left" -eq 0 ] && echo "Nothing is left of $RDS_NAME"
exit "$left"
