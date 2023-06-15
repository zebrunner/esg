#!/bin/bash
# This script prints information about all tasks

# get base directory
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all tasks
. $BASEDIR/list-tasks.sh

describe() {
  local tasks=$1

  aws ecs describe-tasks --cluster $AWS_CLUSTER --tasks $tasks | jq '.tasks[] | [ [{key:.taskArn, value:{containerInstanceArn, group,createdAt,desiredStatus,lastStatus,healthStatus, cpu, memory, containers: [.containers[] | {name,lastStatus}]}}] | from_entries ]'
}

# counter of concatenated tasks arns for describe-tasks command (max 100 per call)
i=0
tasks=
for taskArn in "${tasksArns[@]}"; do
  # example of the taskArn:
  # arn:aws:ecs:us-east-1:659932254483:task/esg-dev/50d8fcf7a7e24adeb4dca2fda5b600d7
  # example of the taskId:
  # 50d8fcf7a7e24adeb4dca2fda5b600d7
  taskId=`echo ${taskArn} | cut -d '/' -f 3`
  
  if [ -z "$tasks" ]; then
    tasks="$taskId"
  else
    tasks="$tasks $taskId"
  fi

  i=$((i+1))
  # making describe call if reached 100 concatenated tasks
  if [[ $i -eq 100 ]]; then
    describe "$tasks"
    i=0
    tasks=""
  fi
done

if [ ! -z "$tasks" ]; then
    describe "$tasks"
fi
