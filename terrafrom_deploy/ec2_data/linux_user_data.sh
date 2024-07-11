#!/bin/bash

#Amazon ECS stores logs in the /var/log/ecs folder of your container instances
echo ECS_CLUSTER=${cluster_name} >> /etc/ecs/ecs.config
echo ECS_LOGLEVEL=info >> /etc/ecs/ecs.config
echo ECS_LOGLEVEL_ON_INSTANCE=info >> /etc/ecs/ecs.config
echo ECS_AVAILABLE_LOGGING_DRIVERS="[\"json-file\",\"awslogs\"]" >> /etc/ecs/ecs.config
echo ECS_AWSVPC_ADDITIONAL_LOCAL_ROUTES=[\"${cidr_block}\"] >> /etc/ecs/ecs.config
echo ECS_ENABLE_TASK_ENI=true >> /etc/ecs/ecs.config

#https://aws.amazon.com/blogs/containers/graceful-shutdowns-with-ecs/
echo ECS_CONTAINER_STOP_TIMEOUT=15s >> /etc/ecs/ecs.config
echo ECS_ENGINE_TASK_CLEANUP_WAIT_DURATION=5m >> /etc/ecs/ecs.config
echo ECS_ENABLE_TASK_IAM_ROLE=true >> /etc/ecs/ecs.config
echo ECS_ENABLE_CONTAINER_METADATA=true >> /etc/ecs/ecs.config
echo ECS_AWSVPC_BLOCK_IMDS=true >> /etc/ecs/ecs.config
echo ECS_ENABLE_SPOT_INSTANCE_DRAINING=true >> /etc/ecs/ecs.config
echo ECS_PULL_DEPENDENT_CONTAINERS_UPFRONT=true >> /etc/ecs/ecs.config

#ECS_IMAGE_PULL_BEHAVIOR <default | always | once | prefer-cached >
echo ECS_IMAGE_PULL_BEHAVIOR=prefer-cached >> /etc/ecs/ecs.config
