#!/bin/bash
# This script stops all tasks for cluster from cluster.sh script`

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all tasks
. $BASEDIR/list-tasks.sh

# add region to command if present
stopTask="aws ecs stop-task --cluster $AWS_CLUSTER"
if [ ! -z "$AWS_REGION" ]; then
  stopTask="$stopTask --region $AWS_REGION"
fi

# iterate tasks by their ARN
echo $TASKS | jq -r '.[]' | while read taskArn ; do
  # example of the taskArn:
  # arn:aws:ecs:us-east-1:659932254483:task/esg-dev/50d8fcf7a7e24adeb4dca2fda5b600d7

  $stopTask --task $taskArn --reason "Stopped by admin" | jq '.[] | [{key:.taskArn, value: {containerInstanceArn, group, createdAt, stoppingAt, desiredStatus, lastStatus, stoppedReason, cpu, memory, containers: [.containers[] | {name,lastStatus}]}}] | from_entries'
  echo
done
