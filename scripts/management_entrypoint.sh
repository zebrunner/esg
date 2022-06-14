#!/bin/sh

./management \
  -aws-cluster esg-dev \
  -aws-elastic-cache redis:6379 \
  -aws-auto-scaling-group esg-dev-asg \
  -log-level debug
