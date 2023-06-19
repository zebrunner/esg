#!/bin/bash
# This script prints information about all tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# get all tasks
. $BASEDIR/list-tasks.sh

# add region to command if present
describeTasks="aws ecs describe-tasks --cluster $AWS_CLUSTER"
describeContainerInstances="aws ecs describe-container-instances --cluster $AWS_CLUSTER"
describeInstances="aws ec2 describe-instances"
if [ ! -z "$AWS_REGION" ]; then
  describeTasks="$describeTasks --region $AWS_REGION"
  describeContainerInstances="$describeContainerInstances --region $AWS_REGION"
  describeInstances="$describeInstances --region $AWS_REGION"
fi

describe() {
  local tasks=$1
  
  # describe tasks by passed taskIds
  tasksDescriptionResponse=`$describeTasks --tasks $tasks | jq '.tasks[] | [[{ key:.taskArn, value:{containerInstanceArn, group,createdAt,desiredStatus,lastStatus,healthStatus, cpu, memory, containers: [.containers[] | (if .networkBindings != null and .networkBindings != [] then {name, lastStatus, networkBindings: [ .networkBindings[] | {containerPort, hostPort}]} else {name, lastStatus} end)]}}] | from_entries ]' | jq -s 'flatten(1)'`
  
  # select unique containerInstanceArn from tasksDescriptionResponse and add them to array
  readarray -t containerInstanceIds < <(echo ${tasksDescriptionResponse} | jq -r '[.[][].containerInstanceArn] | unique | map(select(.!= null)) | .[]')
  # unite all containerInstanceArn into one string
  containerInstances=
  for containerInstanceId in "${containerInstanceIds[@]}"; do
    if [ -z "$containerInstances" ]; then
      containerInstances="$containerInstanceId"
    else
      containerInstances="$containerInstances $containerInstanceId"
    fi
  done
  
  # create map describe-container-instances call, where key - containerInstanceArn, value - ec2InstanceId
  arnIdInstanceArrMap=`$describeContainerInstances --container-instances $containerInstances | jq ' .containerInstances[] | [{key:.containerInstanceArn, value: .ec2InstanceId}] | from_entries | [.]' | jq -s 'flatten(1)'`

  # select all ec2InstanceId from arnIdInstanceArrMap and add them to array
  readarray -t instancesIds < <(echo ${arnIdInstanceArrMap} | jq -r '.[][]')
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
  instancesDescriptionResponse=`$describeInstances --instance-ids $instances | jq -j '.Reservations[].Instances[] | [ [{key:.InstanceId, value: {InstanceType, LaunchTime, Zone: .Placement.AvailabilityZone, PublicIpAddress, PrivateIpAddress, State:.State.Name, Architecture, }}]| from_entries ]' | jq -s 'flatten(1)'`

  # iterate by every task description, so we can add information about instance
  echo "$tasksDescriptionResponse" | jq -c '.[]' | while read -r taskDescription; do
    containerInstanceArn=`echo ${taskDescription} | jq '.[].containerInstanceArn'`

    # containerInstanceArn could be null when task is pending to start
    if [ "$containerInstanceArn" = "null" ]; then
      echo "$taskDescription" | jq '.'
    else
      # find corresponding ec2InstanseId for containerInstanceArn from map
      correspondingEc2InstanceId=`echo ${arnIdInstanceArrMap} | jq 'map(select(.'${containerInstanceArn}')) | .[][]'`
      # find ec2Instance description from instancesDescriptionResponse array by ec2InstanseId
      ec2Description=`echo ${instancesDescriptionResponse} | jq 'map(select(.'${correspondingEc2InstanceId}')) | .[]'`
      # add ec2Instance description to task description and print it
      jq -s 'add' <(echo "$taskDescription") <(echo "$ec2Description")
    fi
    echo -e
  done
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
