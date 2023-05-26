#!/bin/bash
# This script prints cluster value from router.env

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"
echo $AWS_CLUSTER
