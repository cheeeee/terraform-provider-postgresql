#!/bin/bash

source "$(pwd)"/tests/switch_superuser.sh
docker compose -f "$(pwd)"/tests/docker-compose.yml up -d --wait --build  # --wait: the healthchecks of the database and of the publisher
