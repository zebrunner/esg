#!/bin/bash
# This script prints all cluster's tasks

# find out cluster name 
cluster=`scripts/./cluster.sh`

#get all cluster's tasks
aws ecs list-tasks --cluster $cluster