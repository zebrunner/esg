#!/bin/bash
# This script prints information about all instances

# get base directory
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all instances and cluster
. $BASEDIR/list-instances.sh

describe() {
  local instances=$1
  response=`aws ecs describe-container-instances --cluster $AWS_CLUSTER --container-instances $instances  | jq -j '.containerInstances[] | [{key:.containerInstanceArn, value: {ec2InstanceId, status, runningTasksCount, pendingTasksCount, agentConnected, remainingResources: [.remainingResources[] | select(.name == "CPU" or .name == "MEMORY") | {name:.name, integerValue: .integerValue}],registeredResources: [.registeredResources[] | select(.name == "CPU" or .name == "MEMORY") | {name:.name, integerValue: .integerValue}]}}] | from_entries '`

  instanceId=`echo ${response} | jq -r '.[].ec2InstanceId'`
  instance=`aws ec2 describe-instances --instance-ids $instanceId | jq -j '.Reservations[].Instances[] | [{key:.InstanceId, value: {InstanceType, LaunchTime, Zone: .Placement.AvailabilityZone, PublicIpAddress, PrivateIpAddress, State:.State.Name, Architecture, }}]| from_entries'`

  jq -s 'add' <(echo "$response") <(echo "$instance")
}

# counter of concatenated instances arns for describe-instances command (max 100 per call)
for instanceArn in "${instancesArns[@]}"; do
  # example of the instanceArn:
  # arn:aws:ecs:us-east-1:659932254483:container-instance/esg-dev/d085f4e3d2254973beba21d11d7ad105
  # example of the instanceId:
  # d085f4e3d2254973beba21d11d7ad105
  instanceId=`echo ${instanceArn} | cut -d '/' -f 3`
  describe "$instanceId"
  # sleep should deal with throttling as we make 2 calls per instanceId in describe function
  sleep 0.5
done

