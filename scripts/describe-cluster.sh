#!/bin/bash
# This script prints all cluster's tasks

# get base directory and cluster
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../properties/config.env"

# add region to command if present
describeCluster="aws ecs describe-clusters --cluster $AWS_CLUSTER --include ATTACHMENTS"
if [ ! -z "$AWS_REGION" ]; then
  describeCluster="$describeCluster --region $AWS_REGION"
fi

$describeCluster
