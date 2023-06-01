#!/bin/bash
# This script prints full information about all tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all tasks
. $BASEDIR/list-tasks.sh


describe() {
 local tasks=$1
 if [ ! -z "$tasks" ]; then
   aws ecs describe-tasks --cluster $AWS_CLUSTER --tasks $tasks | jq '.tasks[] | [.taskArn, "Created at:", .createdAt, "Health:",.healthStatus, "Last status:", .lastStatus, "Desired status:", .desiredStatus]'
 else
   echo "Tasks not found for cluster $AWS_CLUSTER"
 fi
}

# parse Arns into array
readarray -t tasksArns < <(echo ${TASKS} | jq -j '.[]')
# counter of concatenated tasks arns for describe-tasks command (max 100 per call)
tasks=
i=0
for taskArn in "${tasksArns[@]}"; do
  # example of the taskArn:
  # arn:aws:ecs:us-east-1:659932254483:task/esg-dev/50d8fcf7a7e24adeb4dca2fda5b600d7
  taskId=`echo ${taskArn} | cut -d '/' -f 3 | cut -d '"' -f 1`
  #echo taskId: $taskId

  if [ "$taskId" = "[" ]; then
    continue
  fi

  if [ "$taskId" = "]" ] || [ "$taskId" = "[]" ]; then
    # end of tasks or empty list
    break
  fi

  tasks="$tasks $taskId"
  i=$((i+1))
  # making describe call if reached 100 concatenated tasks
  if [[ $i -eq 100 ]]; then
    describe "$tasks"
    i=0
    tasks=""
  fi
done

describe "$tasks"
