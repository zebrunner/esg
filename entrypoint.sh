#!/bin/sh

export REVISION=$(git rev-parse HEAD)
export BUILD_TIME=$(date -Iseconds)
export VERSION=$(git describe --tags `git rev-list --tags --max-count=1`)

# Apply all up migrations
DATABASE="postgres://postgres:postgres@db/postgres?sslmode=disable"
migrate -path migrations -database $DATABASE up

# Start the first process
nohup ./esg \
    -retry-count 2 \
    -aws-retry 20 \
    -aws-cluster esg-dev \
    -aws-elastic-cache redis:6379 \
    -aws-auto-scaling-group esg-dev-asg \
    -db-connection $DATABASE \
    -listen :4444 \
    -log-level debug \
    -s3-bucket zebrunner.dev-assets >> ./esg.log 2>&1 &
status=$?
if [ $status -ne 0 ]; then
  echo "Failed to start esg:4444: $status"
  exit $status
fi

# Start the second process
nohup ./esg \
    -retry-count 2 \
    -aws-retry 20 \
    -aws-cluster esg-dev \
    -aws-elastic-cache redis:6379 \
    -aws-auto-scaling-group esg-dev-asg \
    -db-connection $DATABASE \
    -listen :4445 \
    -log-level debug \
    -s3-bucket zebrunner.dev-assets >> ./esg.log 2>&1 &
status=$?
if [ $status -ne 0 ]; then
  echo "Failed to start esg:4445: $status"
  exit $status
fi

tail -f esg.log
