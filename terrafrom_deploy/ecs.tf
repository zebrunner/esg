resource "aws_ecs_capacity_provider" "e3s_linux" {
  name = local.e3s_linux_capacityprovider
  auto_scaling_group_provider {
    auto_scaling_group_arn         = aws_autoscaling_group.linux.arn
    managed_termination_protection = "DISABLED"
    managed_scaling {
      status                    = "ENABLED"
      target_capacity           = 100
      minimum_scaling_step_size = 1
      maximum_scaling_step_size = 100
      instance_warmup_period    = 300
    }
  }
}

resource "aws_ecs_capacity_provider" "e3s_windows" {
  name = local.e3s_windows_capacityprovider
  auto_scaling_group_provider {
    auto_scaling_group_arn         = aws_autoscaling_group.windows.arn
    managed_termination_protection = "DISABLED"
    managed_scaling {
      status                    = "ENABLED"
      target_capacity           = 100
      minimum_scaling_step_size = 1
      maximum_scaling_step_size = 100
      instance_warmup_period    = 300
    }
  }
}

resource "aws_ecs_cluster" "e3s" {
  name = local.e3s_cluster_name
}

resource "aws_ecs_cluster_capacity_providers" "attachment" {
  cluster_name       = aws_ecs_cluster.e3s.name
  capacity_providers = [aws_ecs_capacity_provider.e3s_linux.name, aws_ecs_capacity_provider.e3s_windows.name]
  default_capacity_provider_strategy {
    weight            = 1
    capacity_provider = aws_ecs_capacity_provider.e3s_linux.name
  }

  provisioner "local-exec" {
    command = <<-EOF
ecsPolicy=`aws ecs describe-clusters --region ${var.region} --cluster ${aws_ecs_cluster.e3s.name} --include ATTACHMENTS --query 'clusters[0].attachments[].details[]' | grep "${aws_ecs_capacity_provider.e3s_windows.name}" -A 4 | grep "ECSManagedAutoScalingPolicy" | cut -d ':' -f 2 | cut -d '"' -f 2`
aws autoscaling put-scaling-policy --region ${var.region} --auto-scaling-group-name ${aws_autoscaling_group.windows.name} --policy-name "$ecsPolicy" --policy-type TargetTrackingScaling --target-tracking-configuration "{ \"CustomizedMetricSpecification\": { \"MetricName\": \"CapacityProviderReservation\", \"Namespace\": \"AWS/ECS/ManagedScaling\", \"Dimensions\": [{ \"Name\": \"CapacityProviderName\", \"Value\": \"${aws_ecs_capacity_provider.e3s_windows.name}\" }, { \"Name\": \"ClusterName\", \"Value\": \"${aws_ecs_cluster.e3s.name}\"}], \"Statistic\": \"Average\"}, \"TargetValue\": 100.0, \"DisableScaleIn\": false }" --no-enabled
EOF
  }

  provisioner "local-exec" {
    command = <<-EOF
ecsPolicy2=`aws ecs describe-clusters --region ${var.region} --cluster ${aws_ecs_cluster.e3s.name} --include ATTACHMENTS --query 'clusters[0].attachments[].details[]' | grep "${aws_ecs_capacity_provider.e3s_linux.name}" -A 4 | grep "ECSManagedAutoScalingPolicy" | cut -d ':' -f 2 | cut -d '"' -f 2`
aws autoscaling put-scaling-policy --region ${var.region} --auto-scaling-group-name ${aws_autoscaling_group.linux.name} --policy-name "$ecsPolicy2" --policy-type TargetTrackingScaling --target-tracking-configuration "{ \"CustomizedMetricSpecification\": { \"MetricName\": \"CapacityProviderReservation\", \"Namespace\": \"AWS/ECS/ManagedScaling\", \"Dimensions\": [{ \"Name\": \"CapacityProviderName\", \"Value\": \"${aws_ecs_capacity_provider.e3s_linux.name}\" }, { \"Name\": \"ClusterName\", \"Value\": \"${aws_ecs_cluster.e3s.name}\"}], \"Statistic\": \"Average\"}, \"TargetValue\": 100.0, \"DisableScaleIn\": false }" --no-enabled
EOF
  }
}
