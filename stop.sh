#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

docker compose -f "$BASEDIR/docker-compose.yaml" down
