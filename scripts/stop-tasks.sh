#!/bin/bash
# This script stops all tasks for specified in router.env file aws cluster

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# find out cluster name
cluster=`$BASEDIR/./cluster.sh`
# get all tasks
tasks=`$BASEDIR/./list-tasks.sh`

# iterate tasks by their ARN
echo $tasks | jq -r '.[]' | while read taskArn ; do
    taskIdIndex=`expr match $taskArn ^\"[a-z:0-9\-]*/$cluster/`
    if [[ $taskIdIndex != 0 ]]
    then 
        # parse taskId from full ARN
        tmpTaskId=${taskArn: taskIdIndex}
        taskId=${tmpTaskId%%\"*}

        # stop task by taskId for certain cluster
        response= aws ecs stop-task --cluster $cluster --task $taskId | jq '.task.taskArn,
                                                                             "Last status:", .task.lastStatus,
                                                                             "Desired status:", .task.desiredStatus,
                                                                             "Stopped reason:", .task.stoppedReason'
        echo "$response"
    fi
done
