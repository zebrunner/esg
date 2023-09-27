#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# stop services
docker compose -f "$BASEDIR/../docker-compose.yaml" down -t 540
# stop postgres and redis
docker compose -f "$BASEDIR/docker-compose.yaml" down
