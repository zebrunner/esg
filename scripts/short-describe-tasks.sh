#!/bin/bash
# Get short information about all tasks

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# find out cluster name
cluster=`$BASEDIR/./cluster.sh`
# get all tasks
tasks=`$BASEDIR/./list-tasks.sh`
# parse Arns into array
readarray -t tasksArns < <(echo $tasks | jq -j '.[]')
# counter of concatenated tasks arns for describe-tasks command (max 100 per call)
i=0
for taskArn in "${tasksArns[@]}"; do
    taskIdIndex=`expr match $taskArn ^\"[a-z:0-9\-]*/$cluster/`
    if [[ $taskIdIndex != 0 ]]
    then 
        # parse taskId from full ARN
        tmpTaskId=${taskArn: taskIdIndex + 2}
        taskId=${tmpTaskId%%\"*}
        
        # concatenate task and increase counter by one
        tasksStr="$tasksStr $taskId"
        i=$((i+1))    
        
        # making describe call if reached 100 concatenated tasks
        if [[ $i -eq 100 ]]; then
            # making call and parsing response 
            aws ecs describe-tasks --cluster $cluster --tasks $tasksStr | jq '.tasks[] |
                                                                     [.taskArn,
                                                                     "Created at:", .createdAt, 
                                                                     "Health:",.healthStatus, 
                                                                     "Last status:", .lastStatus, 
                                                                     "Desired status:", .desiredStatus, 
                                                                     "Containers:", ( .containers[] | 
                                                                        [ .name, 
                                                                        "Health:",.healthStatus, 
                                                                        "Last status:", .lastStatus, 
                                                                        "Image:", .image ]),]'
            i=0
            tasksStr=""
        fi
    fi
done

if [[ $tasksStr = "" ]];
then
    echo "Tasks not found for cluster $cluster"
else
    aws ecs describe-tasks --cluster $cluster --tasks $tasksStr | jq '.tasks[] |
                                                            [.taskArn,
                                                            "Created at:", .createdAt, 
                                                            "Health:",.healthStatus, 
                                                            "Last status:", .lastStatus, 
                                                            "Desired status:", .desiredStatus, 
                                                            "Containers:", ( .containers[] | 
                                                            [ .name, 
                                                            "Health:",.healthStatus, 
                                                            "Last status:", .lastStatus, 
                                                            "Image:", .image ]) ]'
fi