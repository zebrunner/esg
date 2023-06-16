#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"

aws ecs describe-clusters --cluster $AWS_CLUSTER
