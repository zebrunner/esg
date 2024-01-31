# Prerequisites

## Esg instance

### Hardware

* Instance type m5n.large +
* Configured IMDSv2 (HttpPutResponseHopLimit=2)

### Software

* Installed Docker v19+
* Installed Docker compose plugin v2+
* [Optional] Installed jq and aws cli for ./scripts support

### Attached IAM Role/Policies:

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
          "arn:aws:ecs:${Region}:${Account}:container-instance/esg-${env}/*",
          "arn:aws:ecs:${Region}:${Account}:task/esg-${env}/*",
          "arn:aws:ecs:${Region}:${Account}:cluster/esg-${env}",
          "arn:aws:ecs:${Region}:${Account}:task-definition/${env}-*",
          "arn:aws:ecs:${Region}:${Account}:capacity-provider/esg-${env}-*"
      ]
  },
  {
      "Sid": "Autoscaling",
      "Effect": "Allow",
      "Action": [
          "autoscaling:UpdateAutoScalingGroup",
          "autoscaling:TerminateInstanceInAutoScalingGroup"
      ],
      "Resource": "arn:aws:autoscaling:${Region}:${Account}:autoScalingGroup:*:autoScalingGroupName/esg-${env}-*"
  },
  {
      "Sid": "ELB",
      "Effect": "Allow",
      "Action": [
          "elasticloadbalancing:RegisterTargets",
          "elasticloadbalancing:DeregisterTargets"
      ],
      "Resource": "arn:aws:elasticloadbalancing:${Region}:${Account}:targetgroup/esg-${env}-*"
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
      "Resource": "arn:aws:iam::${Account}:role/esg-${env}-task-role"
  }
}
```

## Agent instance

### Hardware

* Instance type c5a.2xlarge +
* Configured IMDSv2 (HttpPutResponseHopLimit=1)

### Attached IAM Role/Policies:

#### Agent role
```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Sid": "WithoutConstraints",
            "Effect": "Allow",
            "Action": [
                "ecs:DiscoverPollEndpoint",
                "logs:CreateLogStream",
                "logs:PutLogEvents"
            ],
            "Resource": "*"
        },
        {
            "Sid": "ECS",
            "Effect": "Allow",
            "Action": [
                "ecs:DeregisterContainerInstance",
                "ecs:RegisterContainerInstance",
                "ecs:Submit*",
                "ecs:StartTelemetrySession",
                "ecs:UpdateContainerInstancesState",
                "ecs:Poll"
            ],
            "Resource": [
                "arn:aws:ecs:${Region}:${Account}:cluster/esg-${env}",
                "arn:aws:ecs:${Region}:${Account}:container-instance/esg-${env}/*"
            ]
        }
    ]
}
```

#### Task role
```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "s3:PutObject"
            ],
            "Resource": [
                "arn:aws:s3:::zebrunner.${env}-engine/*"
            ]
        }
    ]
}
```