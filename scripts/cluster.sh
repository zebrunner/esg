#!/bin/bash
# This script returns cluster value from router.env
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
clusterLine=$(grep "AWS_CLUSTER" $BASEDIR/../router.env)
echo ${clusterLine: `expr match $clusterLine "AWS_CLUSTER="`}
