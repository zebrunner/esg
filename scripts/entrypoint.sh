#!/bin/sh

export REVISION=$(git rev-parse HEAD)
export BUILD_TIME=$(date -Iseconds)
export VERSION=$(git describe --tags $(git rev-list --tags --max-count=1))

# Apply all up migrations
DATABASE="postgres://postgres:postgres@db/postgres?sslmode=disable"
migrate -path migrations -database $DATABASE up


./server \
  -retry-count 2 \
  -aws-retry 2 \
  -aws-cluster esg-dev \
  -aws-elastic-cache redis:6379 \
  -aws-auto-scaling-group esg-dev-asg \
  -db-connection $DATABASE \
  -listen :4444 \
  -log-level debug \
  -s3-bucket zebrunner.dev-assets
