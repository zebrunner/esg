#!/bin/sh

export REVISION=$(git rev-parse HEAD)
export BUILD_TIME=$(date -Iseconds)
export VERSION=$(git describe --tags $(git rev-list --tags --max-count=1))

# Apply all up migrations
DATABASE="postgres://postgres:postgres@db/postgres?sslmode=disable"
migrate -path migrations -database $DATABASE up


./server \
  -aws-retry 2 \
  -aws-cluster esg-dev \
  -aws-elastic-cache redis:6379 \
  -db-connection $DATABASE \
  -listen :4444 \
  -log-level trace \
  -s3-bucket zebrunner.dev-assets
