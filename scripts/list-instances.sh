#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"

# add region to command if present
listInstances="aws ecs list-container-instances --cluster $AWS_CLUSTER"
if [ ! -z "$AWS_REGION" ]; then
  listInstances="$listInstances --region $AWS_REGION"
fi

#get all cluster's container instances
CONTAINER_INSTANCES=`$listInstances | jq -r '[.containerInstanceArns[]]'`

# parse Arns into array
readarray -t containerInstancesArns < <(echo ${CONTAINER_INSTANCES} | jq -r '.[]')
containerInstances=
for containerInstanceArn in "${containerInstancesArns[@]}"; do
  # example of the containerInstanceArn:
  # arn:aws:ecs:us-east-1:659932254483:container-instance/esg-dev/d085f4e3d2254973beba21d11d7ad105
  if [ -z "$containerInstances" ]; then
    containerInstances="$containerInstanceArn"
  else
    containerInstances="$containerInstances\n$containerInstanceArn"
  fi
done

if [ ! -z "$containerInstances" ]; then
  echo "Container Instances:"
  echo -e $containerInstances
else
  echo "Container Instances not found for cluster $AWS_CLUSTER"
fi
