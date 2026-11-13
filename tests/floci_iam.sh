#!/bin/bash
# The IAM authentication of the provider (tests/e2e/rds_iam.sh) against Floci, a local
# emulator of AWS (https://github.com/floci-io/floci): no AWS account and no cost.
#
# The "RDS instance" of the emulator is a PostgreSQL container behind a proxy which checks
# the token like RDS does: its signature, its region, the endpoint it was made for and the
# session of an assumed role. The roles and the credentials come from its IAM and its STS.
#
# It is NOT AWS RDS: the master user is a superuser, there is no rdsadmin and no parameter
# group. What only RDS does is tested by rds_test.sh and rds_e2e.sh on a real instance.
#
# The emulator starts the database with the Docker daemon of the host: it needs its socket.
#
#   PGVERSION      major version of PostgreSQL (default: 17)
#   FLOCI_IMAGE    image of the emulator
#   FLOCI_PORT     port of the emulator on the host (default: 4566)
#   FLOCI_RDS_PORT port of the instance on the host (default: 7001)
#   FLOCI_NAME     name of the container and of the instance, for several runs at the same time
#   E2E_TERRAFORM  set it to tofu to run it with OpenTofu
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

PGVERSION=${PGVERSION:-17}
# The image is the one of this digest (version 2.4.0): it is given the socket of Docker
FLOCI_IMAGE=${FLOCI_IMAGE:-floci/floci:2.4.0@sha256:5ee85db604c4aa74ac76e07ea5cc83086f37bb472bf3dbb97e34019e48efae9c}
FLOCI_PORT=${FLOCI_PORT:-4566}
FLOCI_RDS_PORT=${FLOCI_RDS_PORT:-7001}
CONTAINER=${FLOCI_NAME:-tfpg-floci}
INSTANCE=$CONTAINER

WORK=$(mktemp -d)

# Nothing of a real AWS account can be reached from here: no profile, no shared
# credentials, and the credentials and the endpoint of the emulator.
: > "$WORK/config"
: > "$WORK/credentials"
unset AWS_PROFILE AWS_DEFAULT_PROFILE AWS_SESSION_TOKEN AWS_DEFAULT_REGION
export AWS_CONFIG_FILE="$WORK/config" AWS_SHARED_CREDENTIALS_FILE="$WORK/credentials" AWS_EC2_METADATA_DISABLED=true
export AWS_ENDPOINT_URL="http://localhost:$FLOCI_PORT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
# Not the default region of anything
export AWS_REGION=eu-west-3 AWS_PAGER=""

cleanup() {
    status=$?
    set +e
    aws rds delete-db-instance --db-instance-identifier "$INSTANCE" --skip-final-snapshot > /dev/null 2>&1
    docker rm -f "$CONTAINER" > /dev/null 2>&1
    # What the emulator started and could not remove itself
    left=$(docker ps -aq --filter label=io.floci=aws --filter "label=io.floci.resource-id=$INSTANCE")
    if [ -n "$left" ]; then
        docker rm -f -v $left > /dev/null 2>&1
    fi
    rm -rf "$WORK"
    exit "$status"
}
trap cleanup EXIT

echo "## Starting the emulator ($FLOCI_IMAGE)"
docker rm -f "$CONTAINER" > /dev/null 2>&1 || true
docker run -d --name "$CONTAINER" -u root \
    -p "$FLOCI_PORT:4566" -p "$FLOCI_RDS_PORT:$FLOCI_RDS_PORT" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e FLOCI_SERVICES_RDS_PROXY_BASE_PORT="$FLOCI_RDS_PORT" -e FLOCI_SERVICES_RDS_PROXY_MAX_PORT="$FLOCI_RDS_PORT" \
    -e FLOCI_SERVICES_RDS_ENDPOINT_HOST=localhost \
    "$FLOCI_IMAGE" > /dev/null
for attempt in $(seq 60); do
    if curl -fsS -o /dev/null "$AWS_ENDPOINT_URL/_floci/health" 2> /dev/null; then
        break
    fi
    if [ "$attempt" = 60 ]; then
        docker logs "$CONTAINER" 2>&1 | tail -n 20
        echo "FAIL: the emulator did not start"
        exit 1
    fi
    sleep 1
done

password=$(openssl rand -hex 16)
echo "## Creating the instance $INSTANCE (PostgreSQL $PGVERSION, IAM authentication)"
aws rds create-db-instance --db-instance-identifier "$INSTANCE" --db-instance-class db.t3.micro \
    --engine postgres --engine-version "$PGVERSION" --allocated-storage 20 \
    --master-username tfadmin --master-user-password "$password" \
    --enable-iam-database-authentication > /dev/null
for attempt in $(seq 90); do
    state=$(aws rds describe-db-instances --db-instance-identifier "$INSTANCE" --query 'DBInstances[0].DBInstanceStatus' --output text)
    if [ "$state" = available ]; then
        break
    fi
    if [ "$attempt" = 90 ]; then
        docker logs "$CONTAINER" 2>&1 | tail -n 20
        echo "FAIL: the instance is $state"
        exit 1
    fi
    sleep 2
done
port=$(aws rds describe-db-instances --db-instance-identifier "$INSTANCE" --query 'DBInstances[0].Endpoint.Port' --output text)

export PGHOST=localhost PGPORT=$port PGUSER=tfadmin PGPASSWORD=$password
# Use psql from a container if it's not installed locally
if ! command -v psql > /dev/null; then
    export E2E_PSQL="docker run --rm --add-host=host.docker.internal:host-gateway -e PGPASSWORD=$password -e PGSSLMODE=require postgres:${PGVERSION}-alpine psql -h host.docker.internal -p $port -U tfadmin"
fi

# The proxy of the emulator refuses a token with the message of a wrong password
export IAM_REFUSED="password authentication failed"
export RDS_NAME=$INSTANCE RDS_IAM_ROLE=$INSTANCE-connect RDS_IAM_USER=$INSTANCE-assumer
"$HERE/e2e/rds_iam.sh"
