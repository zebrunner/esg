#!/bin/bash
# This script returns cluster value from router.env
clusterLine=$(grep "AWS_CLUSTER" router.env)
echo ${clusterLine: `expr match $clusterLine "AWS_CLUSTER="`}
