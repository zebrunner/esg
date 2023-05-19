#!/bin/bash
# This script stops all tasks for specified in router.env file aws cluster

# find out cluster name
clusterLine=`grep "AWS_CLUSTER" ../router.env`
cluster=${clusterLine: `expr match $clusterLine AWS_CLUSTER=`}

# get all tasks for current cluster and iterate by their AKR
aws ecs list-tasks --cluster $cluster | jq -r '.[]' | while read taskAkr ; do
    taskIdIndex=`expr match $taskAkr ^\"[a-z:0-9\-]*/$cluster/`
    if [[ $taskIdIndex != 0 ]]
    then 
        # parse taskId from full AKR
        tmpTaskId=${taskAkr: taskIdIndex}
        taskId=${tmpTaskId%%\"*}
        # stop task by taskId for certain cluster
        response= aws ecs stop-task --cluster $cluster --task $taskId | jq '.task.taskArn, .task.lastStatus'
        echo "$response"
    fi
done
