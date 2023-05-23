#!/bin/bash
# This script prints all cluster's tasks

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# find out cluster name 
cluster=`$BASEDIR/./cluster.sh`

#get all cluster's tasks
aws ecs list-tasks --cluster $cluster