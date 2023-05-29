#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" 
cluster=`$BASEDIR/cluster.sh`

#get all cluster's tasks
aws ecs list-tasks --cluster $cluster