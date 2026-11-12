#!/bin/bash
# Create a temporary AWS RDS instance, reachable only from the current public IP,
# with pgaudit loaded. It has to be deleted with rds_down.sh.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/rds_common.sh"

family="postgres${PGVERSION}"
version=$(aws rds describe-db-engine-versions --engine postgres \
    --query "DBEngineVersions[?DBParameterGroupFamily=='${family}'].EngineVersion | [-1]" --output text)
if [ -z "$version" ] || [ "$version" = "None" ]; then
    echo "PostgreSQL $PGVERSION is not available on RDS in $AWS_REGION" >&2
    exit 1
fi

vpc=$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId' --output text)
if [ -z "$vpc" ] || [ "$vpc" = "None" ]; then
    echo "There is no default VPC in $AWS_REGION" >&2
    exit 1
fi
ip=$(curl -fsS --max-time 10 https://checkip.amazonaws.com)

echo "Creating security group $RDS_NAME (PostgreSQL allowed from $ip only)"
sg=$(aws ec2 create-security-group --group-name "$RDS_NAME" --vpc-id "$vpc" \
    --description "terraform-provider-postgresql tests (temporary)" \
    --tag-specifications "ResourceType=security-group,Tags=[{Key=purpose,Value=tfpg-test}]" \
    --query GroupId --output text)
aws ec2 authorize-security-group-ingress --group-id "$sg" --protocol tcp --port 5432 --cidr "${ip}/32" > /dev/null

echo "Creating parameter group $RDS_NAME ($family)"
aws rds create-db-parameter-group --db-parameter-group-name "$RDS_NAME" --db-parameter-group-family "$family" \
    --description "terraform-provider-postgresql tests (temporary)" --tags Key=purpose,Value=tfpg-test > /dev/null
aws rds modify-db-parameter-group --db-parameter-group-name "$RDS_NAME" --parameters \
    "ParameterName=shared_preload_libraries,ParameterValue='pgaudit,pg_stat_statements',ApplyMethod=pending-reboot" \
    "ParameterName=pgaudit.role,ParameterValue=rds_pgaudit,ApplyMethod=pending-reboot" \
    "ParameterName=rds.logical_replication,ParameterValue=1,ApplyMethod=pending-reboot" > /dev/null

password=$(openssl rand -hex 16)

echo "Creating instance $RDS_NAME (PostgreSQL $version)"
aws rds create-db-instance --db-instance-identifier "$RDS_NAME" \
    --engine postgres --engine-version "$version" --db-instance-class "${RDS_INSTANCE_CLASS:-db.t4g.micro}" \
    --allocated-storage 20 --storage-type gp3 \
    --master-username "$RDS_USER" --master-user-password "$password" \
    --vpc-security-group-ids "$sg" --db-parameter-group-name "$RDS_NAME" \
    --publicly-accessible --no-multi-az --backup-retention-period 0 --no-deletion-protection \
    --enable-iam-database-authentication --no-auto-minor-version-upgrade \
    --tags Key=purpose,Value=tfpg-test > /dev/null

echo "Waiting for the instance to be available"
aws rds wait db-instance-available --db-instance-identifier "$RDS_NAME"
host=$(aws rds describe-db-instances --db-instance-identifier "$RDS_NAME" \
    --query 'DBInstances[0].Endpoint.Address' --output text)

(
    umask 077
    cat > "$RDS_STATE" <<STATE
PGHOST=$host
PGPASSWORD=$password
STATE
)
echo "Instance $RDS_NAME is available"
