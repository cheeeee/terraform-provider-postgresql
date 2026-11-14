#!/bin/bash
# Delete what a run of the tests left behind on AWS: the instances, parameter groups,
# security groups and secrets named tfpg-test-* which are older than a run can be.
# (rds_down.sh deletes them at the end of a run; this is for the run which could not.)
#
# It fails when it found something, after having deleted it: something left behind is
# a problem to look at, and it costs money until it is deleted.
#
#   RDS_JANITOR_AGE  age in seconds from which a resource is deleted (default: 3 hours,
#                    a job of the tests lasts less than two)
#   RDS_JANITOR_DRY  set it to 1 to only list what would be deleted
set -uo pipefail

export AWS_REGION=${AWS_REGION:-ca-central-1}
export AWS_PAGER=""
AGE=${RDS_JANITOR_AGE:-10800}
DRY=${RDS_JANITOR_DRY:-0}
PREFIX=tfpg-test-

found=0
failed=0

# old <date>: is this date (seconds since 1970, or ISO 8601) older than AGE? A resource
# without a date is old: the ones created before the dates were recorded.
old() {
    python3 - "$1" "$AGE" <<'PY'
import datetime
import sys

value, age = sys.argv[1], float(sys.argv[2])
if value in ("", "None"):
    sys.exit(0)
try:
    created = float(value)
except ValueError:
    created = datetime.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
sys.exit(0 if datetime.datetime.now(datetime.timezone.utc).timestamp() - created > age else 1)
PY
}

# remove <what> <name> <command which deletes it>
remove() {
    local what=$1 name=$2
    shift 2
    found=1
    if [ "$DRY" = 1 ]; then
        echo "Would delete $what $name"
        return
    fi
    echo "Deleting $what $name"
    if ! "$@" > /dev/null; then
        echo "Could not delete $what $name" >&2
        failed=1
    fi
}

# could_not_list <what>: a list which cannot be read is a failure, not an empty list
could_not_list() {
    echo "Could not list the $1" >&2
    failed=1
}

# --- instances first: their parameter group and their security group are in use until they are gone
instances=$(aws rds describe-db-instances --output text \
    --query "DBInstances[?starts_with(DBInstanceIdentifier, '$PREFIX')].[DBInstanceIdentifier,InstanceCreateTime,DBInstanceStatus]") ||
    could_not_list instances
deleting=()
# The instances which are kept: a run is using them, with the groups of the same name
running=" "
while read -r name created status; do
    [ -n "$name" ] || continue
    if [ "$status" = deleting ]; then
        deleting+=("$name")
    elif old "$created"; then
        remove instance "$name" aws rds delete-db-instance --db-instance-identifier "$name" \
            --skip-final-snapshot --delete-automated-backups
        [ "$DRY" = 1 ] || deleting+=("$name")
    else
        running+="$name "
    fi
done <<< "$instances"
for name in ${deleting[@]+"${deleting[@]}"}; do
    aws rds wait db-instance-deleted --db-instance-identifier "$name" || failed=1
done

# --- parameter groups: their date is in the tag "created" (rds_up.sh)
groups=$(aws rds describe-db-parameter-groups --output text \
    --query "DBParameterGroups[?starts_with(DBParameterGroupName, '$PREFIX')].[DBParameterGroupName,DBParameterGroupArn]") ||
    could_not_list "parameter groups"
while read -r name arn; do
    [ -n "$name" ] || continue
    [[ "$running" != *" $name "* ]] || continue
    if ! created=$(aws rds list-tags-for-resource --resource-name "$arn" --query "TagList[?Key=='created'].Value | [0]" --output text); then
        could_not_list "tags of the parameter group $name"
        continue
    fi
    if old "$created"; then
        remove "parameter group" "$name" aws rds delete-db-parameter-group --db-parameter-group-name "$name"
    fi
done <<< "$groups"

# --- security groups: the ones with the tag of the tests, their date is in the tag "created"
security_groups=$(aws ec2 describe-security-groups --filters Name=tag:purpose,Values=tfpg-test --output text \
    --query "SecurityGroups[].[GroupId,GroupName,Tags[?Key=='created'].Value | [0]]") ||
    could_not_list "security groups"
while read -r id name created; do
    [ -n "$id" ] || continue
    [[ "$running" != *" $name "* ]] || continue
    if old "$created"; then
        remove "security group" "$name ($id)" aws ec2 delete-security-group --group-id "$id"
    fi
done <<< "$security_groups"

# --- secrets
secrets=$(aws secretsmanager list-secrets --filters "Key=name,Values=$PREFIX" --output text \
    --query "SecretList[].[Name,CreatedDate]") ||
    could_not_list secrets
while read -r name created; do
    [ -n "$name" ] || continue
    if old "$created"; then
        remove secret "$name" aws secretsmanager delete-secret --secret-id "$name" --force-delete-without-recovery
    fi
done <<< "$secrets"

if [ "$failed" -ne 0 ]; then
    echo "FAIL: something could not be listed or deleted" >&2
    exit 1
fi
if [ "$found" -ne 0 ]; then
    echo "FAIL: resources of the tests were left behind (see above)" >&2
    exit 1
fi
echo "Nothing is left of the tests in $AWS_REGION"
