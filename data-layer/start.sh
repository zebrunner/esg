#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

networkName="e3s-network"
networkDescription=$(docker network ls -f name=$networkName | grep $networkName)
if [ -z "$networkDescription" ]; then 
  # create network with name $networkName
  docker network create -d bridge "$networkName"
fi

# start postgres and redis
docker compose -f "$BASEDIR/docker-compose.yaml" up -d
# start other services
docker compose -f "$BASEDIR/../docker-compose.yaml" up -d
