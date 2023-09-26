#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# start postgres and redis
docker compose -f "$BASEDIR/docker-compose.yaml" up -d
# start other services
docker compose -f "$BASEDIR/../docker-compose.yaml" up -d --scale router=2
