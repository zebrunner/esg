#!/bin/bash

cluster=""
taskAkrArr= aws ecs list-tasks --cluster $cluster | jq -r '.[]' | while read taskAkr ; do
    taskIdIndex=`expr match $taskAkr ^\"[a-z:0-9\-]*/$cluster/`
    if [[ $taskIdIndex != 0 ]]
    then 
        tmpTaskId=${taskAkr: taskIdIndex}
        taskId=${tmpTaskId%%\"*}
        response= aws ecs stop-task --cluster $cluster --task $taskId | jq '.task.taskArn, .task.lastStatus'
        echo "$response"
    fi
done
