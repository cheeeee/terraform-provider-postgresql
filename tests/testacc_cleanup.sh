#!/bin/bash

source "$(pwd)"/tests/switch_superuser.sh
# -v: also remove the anonymous volume of the database, otherwise every run leaves one behind
docker compose -f "$(pwd)"/tests/docker-compose.yml down -v
unset TF_ACC PGHOST PGPORT PGUSER PGPASSWORD PGSSLMODE PGSUPERUSER
