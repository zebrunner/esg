# Prerequisites

## Esg instance

### Hardware

* Instance type m5n.large +
* Configured IMDSv2 (HttpPutResponseHopLimit=2)

### Software

* Installed Docker v19+
* Installed Docker compose plugin v2+
* (Optinal) Installed jq for ./scripts support
* (Optinal) Installed aws cli for ./scripts support

### Role's actions

* S3 actions
  * ListBucket
  * GetObject
* ECS actions
  * RunTask
  * ListTasks
  * ListContainerInstances
  * RegisterTaskDefinition
  * StopTask
  * DescribeContainerInstances
  * DescribeTasks
  * DescribeClusters
  * DescribeCapacityProviders
* EC2 actions 
  * DescribeInstances
  * DescribeInstanceTypes
  * DescribeInstanceStatus
* ELB actions
  * DescribeLoadBalancer
  * DescribeTargetGroups
  * DeregisterTargets
  * RegisterTargets
* Autoscaling actions
  * DescribeAutoScalingGroups
  * UpdateAutoScalingGroup
  * TerminateInstanceInAutoScalingGroup
* IAM actions
  * passRole

## Agent instance

### Hardware

* Instance type c5a.2xlarge +
* Configured IMDSv2 (HttpPutResponseHopLimit=1)

### Role's policies

* AmazonEC2ContainerServiceforEC2Role
