#!/bin/bash
# This script stops all tasks for cluster from cluster.sh script`

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all tasks
. $BASEDIR/list-tasks.sh

# iterate tasks by their ARN
echo $TASKS | jq -r '.[]' | while read taskArn ; do
  # example of the taskArn:
  # arn:aws:ecs:us-east-1:659932254483:task/esg-dev/50d8fcf7a7e24adeb4dca2fda5b600d7
  taskId=`echo ${taskArn} | cut -d '/' -f 3 | cut -d '"' -f 1`
  #echo taskId: $taskId

  if [ "$taskId" = "[" ] || [ "$taskId" = "]" ] || [ "$taskId" = "[]" ]; then
    continue
  fi

  aws ecs stop-task --cluster $AWS_CLUSTER --task $taskId --reason "Stopped forcibly by admin" | jq '.task.taskArn, "Last status:", .task.lastStatus, "Desired status:", .task.desiredStatus, "Stopped reason:", .task.stoppedReason'
  echo
done
