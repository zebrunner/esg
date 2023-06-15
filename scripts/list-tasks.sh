#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"

#get all cluster's tasks
TASKS=`aws ecs list-tasks --cluster $AWS_CLUSTER | jq -r '[.taskArns[]]'`

# parse Arns into array
readarray -t tasksArns < <(echo ${TASKS} | jq -r '.[]')
tasks=
for taskArn in "${tasksArns[@]}"; do
  # example of the taskArn:
  # arn:aws:ecs:us-east-1:659932254483:task/esg-dev/50d8fcf7a7e24adeb4dca2fda5b600d7
  if [ -z "$tasks" ]; then
    tasks="$taskArn"
  else
    tasks="$tasks\n$taskArn"
  fi
done

if [ ! -z "$tasks" ]; then
  echo "Tasks:"
  echo -e $tasks
else
  echo "Tasks not found for cluster $AWS_CLUSTER"
fi
