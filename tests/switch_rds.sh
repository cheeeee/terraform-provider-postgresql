#!/bin/bash

# (the second expansion is the one of zsh: the script is sourced from the two shells)
# shellcheck disable=SC2046,SC2296
HERE=$(dirname $(readlink -f ${BASH_SOURCE:-${(%):-%N}}))

source "$HERE/switch_superuser.sh"

# Use psql from the postgres container if it's not installed locally
if ! command -v psql > /dev/null; then
    psql() {
        docker compose -f "$HERE/docker-compose.yml" exec -T -e PGUSER="$PGUSER" -e PGPASSWORD="$PGPASSWORD" postgres psql "$@"
    }
fi

echo "Switching to an RDS-like environment"
psql -d postgres  > /dev/null <<EOS
BEGIN;
    CREATE role rds LOGIN CREATEDB CREATEROLE PASSWORD 'rds';
    -- On RDS, postgres user is member of these roles
    -- But it's not really needed for the tests and pg_monitor is
    -- not available on Postgres 8.x
    -- GRANT pg_monitor,pg_signal_backend TO rds;
    ALTER DATABASE postgres OWNER TO rds;
    ALTER SCHEMA public OWNER TO rds;
COMMIT;
EOS

# The master user of a managed service is not a superuser but it is allowed to
# use the replication, to create subscriptions and to set the parameters of pgaudit
# (e.g.: with the rds_superuser role on AWS RDS). The roles and privileges which
# allow it depend on the version of PostgreSQL.
psql -d postgres > /dev/null <<EOS
ALTER ROLE rds REPLICATION;
DO \$\$
BEGIN
    -- EXECUTE: these statements are not valid for the older versions
    IF current_setting('server_version_num')::int >= 160000 THEN
        EXECUTE 'GRANT pg_create_subscription TO rds';
    END IF;
    IF current_setting('server_version_num')::int >= 150000 THEN
        EXECUTE 'GRANT SET ON PARAMETER pgaudit.log TO rds';
    END IF;
END
\$\$;
EOS

psql -d template1 > /dev/null <<EOS
BEGIN;
    ALTER SCHEMA public OWNER TO rds;
COMMIT;
EOS

export TF_ACC=true
export PGHOST=localhost
export PGPORT=${TEST_PGPORT:-25432}
export PGUSER=rds
export PGPASSWORD=rds
export PGSSLMODE=disable
export PGSUPERUSER=false

# The subscriptions of the tests connect to the server itself. A user who is not a superuser
# cannot do it through the trusted local connection of the container ("password is required"):
# the address is the one of the service on the network of Docker, where a password is asked.
export TEST_PUBLISHER_HOST=postgres
