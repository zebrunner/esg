#!/bin/bash
# This script prints information about all instances

# get base directory
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all instances and cluster
. $BASEDIR/list-instances.sh

# add region to commands if present
describeContainerInstances="aws ecs describe-container-instances --cluster $AWS_CLUSTER"
describeInstances="aws ec2 describe-instances"
if [ ! -z "$AWS_REGION" ]; then
  describeContainerInstances="$describeContainerInstances --region $AWS_REGION"
  describeInstances="$describeInstances --region $AWS_REGION"
fi

describe() {
  local containerInstances=$1
  # describe container-instances by cluster and passed container-instances ids 
  containerInstancesDescription=`$describeContainerInstances --container-instances $containerInstances  | jq '.containerInstances[] | [[{key:.containerInstanceArn, value: {ec2InstanceId, status, runningTasksCount, pendingTasksCount, agentConnected, remainingResources: [.remainingResources[] | select(.name == "CPU" or .name == "MEMORY") | {name:.name, integerValue: .integerValue}],registeredResources: [.registeredResources[] | select(.name == "CPU" or .name == "MEMORY") | {name:.name, integerValue: .integerValue}]}}] | from_entries ]' | jq -s 'flatten(1)'` 

  # select unique ec2InstanceId from containerInstancesDescription and add them to array
  readarray -t instancesIds < <(echo ${containerInstancesDescription} | jq -r '[.[][].ec2InstanceId ] | unique | map(select(.!= null)) | .[]')

  # unite all ec2InstanceId into one string
  instances=
  for instanceId in "${instancesIds[@]}"; do
    if [ -z "$instances" ]; then
      instances="$instanceId"
    else
      instances="$instances $instanceId"
    fi
  done

  # describe ec2Instances by cluster and parsed ec2 instance ids 
  instancesArr=`$describeInstances --instance-ids $instances | jq -j '.Reservations[].Instances[] | [[{key:.InstanceId, value: {InstanceType, LaunchTime, Zone: .Placement.AvailabilityZone, PublicIpAddress, PrivateIpAddress, State:.State.Name, Architecture, }}]| from_entries]' | jq -s 'flatten(1)'`

  # iterate by every container instance description, so we can add information about instance
  echo "$containerInstancesDescription" | jq -c '.[]' | while read -r containerInstanceDescription; do
    # find instanceId for containerInstanceDescription
    instanceId=`echo ${containerInstanceDescription} | jq '.[].ec2InstanceId'`
    # find ec2Instance description from instancesArr array by instanceId
    instanceDescription=`echo ${instancesArr} | jq 'map(select(.'${instanceId}')) | .[]'`
    
    # add ec2Instance description to container instance description and print it
    jq -s 'add' <(echo "$containerInstanceDescription") <(echo "$instanceDescription")
    echo -e
  done
}

# counter of concatenated containerInstances arns for describe-container-instances command (max 100 per call)
i=0
containerInstances=
for containerInstanceArn in "${containerInstancesArns[@]}"; do
  # example of the containerInstanceArn:
  # arn:aws:ecs:us-east-1:659932254483:container-instance/esg-dev/d085f4e3d2254973beba21d11d7ad105
  # example of the containerInstanceId:
  # d085f4e3d2254973beba21d11d7ad105
  containerInstanceId=`echo ${containerInstanceArn} | cut -d '/' -f 3`

  if [ -z "$containerInstances" ]; then
    containerInstances="$containerInstanceId"
  else
    containerInstances="$containerInstances $containerInstanceId"
  fi
  i=$((i+1))
  # making describe call if reached 100 concatenated instances
  if [[ $i -eq 100 ]]; then
    describe "$containerInstances"
    i=0
    containerInstances=""
  fi
done

if [ ! -z "$containerInstances" ]; then
    describe "$containerInstances"
fi