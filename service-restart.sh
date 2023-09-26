#!/bin/bash
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

service_name=$1
if [ -z "$service_name" ]; then
  echo "service name is not passed"
  exit 1
fi

docker compose -f "$BASEDIR/docker-compose.yaml" stop $service_name
ret=$?
if [ $ret -ne 0 ]; then 
  echo "failed to stop service $service_name"
  exit 1
fi

docker compose -f "$BASEDIR/docker-compose.yaml" up -d --no-deps $service_name
ret=$?
if [ $ret -ne 0 ]; then 
  echo "failed to start service $service_name"
  exit 1
fi
