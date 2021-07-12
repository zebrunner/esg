#!/bin/sh

./background \
  -aws-retry 2 \
  -aws-cluster esg-dev \
  -aws-elastic-cache redis:6379 \
  -aws-auto-scaling-group esg-dev-asg \
  -log-level debug
