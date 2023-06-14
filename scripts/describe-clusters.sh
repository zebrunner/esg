#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"

CLUSTER_DEF=`aws ecs describe-clusters --cluster $AWS_CLUSTER`

echo "$CLUSTER_DEF"