#!/bin/bash

export TF_ACC=true
export PGHOST=localhost
# TEST_PGPORT: port of the test database on the host (see docker-compose.yml)
export PGPORT=${TEST_PGPORT:-25432}
export PGUSER=postgres
export PGPASSWORD=postgres
export PGSSLMODE=disable
export PGSUPERUSER=true

# The remote publisher of the subscriptions (service "publisher" of docker-compose.yml):
# DSN is how the tests reach it, CONNINFO how the subscriber (the test database) does.
export TEST_REMOTE_PUBLISHER_DSN="postgres://postgres:postgres@localhost:${TEST_PUBLISHER_PGPORT:-25433}/postgres?sslmode=disable"
export TEST_REMOTE_PUBLISHER_CONNINFO="host=publisher port=5432 user=postgres password=postgres"
# (set by switch_rds.sh only)
unset TEST_PUBLISHER_HOST
