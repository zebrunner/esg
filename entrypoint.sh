#!/bin/sh

# Apply all up migrations
DATABASE="postgres://postgres:postgres@db/postgres?sslmode=disable"
migrate -path auth/migrations -database $DATABASE up

# Start the first process
nohup ./esg \
    -retry-count 2 \
    -aws-elastic-cache redis:6379 \
    -db-connection $DATABASE \
    -listen :4444 \
    -s3-bucket zebrunner.dev-assets >> ./esg.log 2>&1 &
status=$?
if [ $status -ne 0 ]; then
  echo "Failed to start esg:4444: $status"
  exit $status
fi

# Start the second process
nohup ./esg \
    -retry-count 2 \
    -aws-elastic-cache redis:6379 \
    -db-connection $DATABASE \
    -listen :4445 \
    -s3-bucket zebrunner.dev-assets >> ./esg.log 2>&1 &
status=$?
if [ $status -ne 0 ]; then
  echo "Failed to start esg:4445: $status"
  exit $status
fi

# Naive check runs checks once a minute to see if either of the processes exited.
# more than one service in a container. The container exits with an error
# if it detects that either of the processes has exited.
# Otherwise it loops forever, waking up every 60 seconds
while sleep 60; do
  ps aux | grep ./esg | grep :4444
  PROCESS_1_STATUS=$?
  ps aux | grep ./esg | grep :4445
  PROCESS_2_STATUS=$?
  # If the greps above find anything, they exit with 0 status
  # If they are not both 0, then something is wrong
  if [ $PROCESS_1_STATUS -ne 0 -o $PROCESS_2_STATUS -ne 0 ]; then
    echo "One of the processes has already exited."
    exit 1
  fi
done
