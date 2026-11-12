#!/bin/bash

export TF_ACC=true
export PGHOST=localhost
# TEST_PGPORT: port of the test database on the host (see docker-compose.yml)
export PGPORT=${TEST_PGPORT:-25432}
export PGUSER=postgres
export PGPASSWORD=postgres
export PGSSLMODE=disable
export PGSUPERUSER=true
