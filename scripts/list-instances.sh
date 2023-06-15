#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"

#get all cluster's instances
INSTANCES=`aws ecs list-container-instances --cluster $AWS_CLUSTER | jq -r '[.containerInstanceArns[]]'`

# parse Arns into array
readarray -t instancesArns < <(echo ${INSTANCES} | jq -r '.[]')
instances=
for instanceArn in "${instancesArns[@]}"; do
  # example of the instanceArn:
  # arn:aws:ecs:us-east-1:659932254483:container-instance/esg-dev/d085f4e3d2254973beba21d11d7ad105
  if [ -z "$instances" ]; then
    instances="$instanceArn"
  else
    instances="$instances\n$instanceArn"
  fi
done

if [ ! -z "$instances" ]; then
  echo "Instances:"
  echo -e $instances
else
  echo "Instances not found for cluster $AWS_CLUSTER"
fi
