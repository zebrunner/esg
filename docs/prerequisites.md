# Prerequisites

## Esg instance

### Hardware

* Instance type m5n.large +
* Configured IMDSv2 (HttpPutResponseHopLimit=2)

### Software

* Installed Docker v19+
* Installed Docker compose plugin v2+
* [Optional] Installed jq for ./scripts support
* [Optional] Installed aws cli for ./scripts support

### Role's policy document:

```json
"Document": {
  {
      "Sid": "WithoutConstraints",
      "Effect": "Allow",
      "Action": [
          "ecs:RegisterTaskDefinition",
          "ecs:ListTasks",
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceStatus",
          "ec2:DescribeInstanceTypes",
          "elasticloadbalancing:DescribeLoadBalancers",
          "elasticloadbalancing:DescribeTargetGroups",
          "autoscaling:DescribeAutoScalingGroups"
      ],
      "Resource": "*"
  },
  {
      "Sid": "ECS",
      "Effect": "Allow",
      "Action": [
          "ecs:DescribeContainerInstances",
          "ecs:DescribeTasks",
          "ecs:StopTask",
          "ecs:DescribeClusters",
          "ecs:ListContainerInstances",
          "ecs:RunTask",
          "ecs:DescribeCapacityProviders"
      ],
      "Resource": [
          "arn:aws:ecs:us-east-1:659932254483:container-instance/esg-${env}/*",
          "arn:aws:ecs:us-east-1:659932254483:task/esg-${env}/*",
          "arn:aws:ecs:us-east-1:659932254483:cluster/esg-${env}",
          "arn:aws:ecs:us-east-1:659932254483:task-definition/${env}-*",
          "arn:aws:ecs:us-east-1:659932254483:capacity-provider/esg-${env}-*"
      ]
  },
  {
      "Sid": "Autoscaling",
      "Effect": "Allow",
      "Action": [
          "autoscaling:UpdateAutoScalingGroup",
          "autoscaling:TerminateInstanceInAutoScalingGroup"
      ],
      "Resource": "arn:aws:autoscaling:us-east-1:659932254483:autoScalingGroup:*:autoScalingGroupName/esg-${env}-*"
  },
  {
      "Sid": "ELB",
      "Effect": "Allow",
      "Action": [
          "elasticloadbalancing:RegisterTargets",
          "elasticloadbalancing:DeregisterTargets"
      ],
      "Resource": "arn:aws:elasticloadbalancing:us-east-1:659932254483:targetgroup/esg-${env}-*"
  },
  {
      "Sid": "S3",
      "Effect": "Allow",
      "Action": [
          "s3:ListBucket",
          "s3:GetObject"
      ],
      "Resource": [
          "arn:aws:s3:::zebrunner.${env}-engine",
          "arn:aws:s3:::zebrunner.${env}-engine/*"
      ]
  },
  {
      "Sid": "IAM",
      "Effect": "Allow",
      "Action": [
          "iam:passRole"
      ],
      "Resource": "arn:aws:iam::659932254483:role/esg-${env}-task-role"
  }
}
```

## Agent instance

### Hardware

* Instance type c5a.2xlarge +
* Configured IMDSv2 (HttpPutResponseHopLimit=1)

### Role's policy

* AmazonEC2ContainerServiceforEC2Role
