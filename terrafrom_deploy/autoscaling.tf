resource "aws_autoscaling_group" "linux" {
  name = local.e3s_linux_autoscaling_name
  mixed_instances_policy {
    launch_template {
      launch_template_specification {
        launch_template_id = aws_launch_template.e3s_linux.id
        version            = aws_launch_template.e3s_linux.latest_version
      }

      // TODO: parametrize as map and implement for each
      override {
        weighted_capacity = 1
        instance_type     = "c5a.2xlarge"
      }

      override {
        weighted_capacity = 2
        instance_type     = "c5a.4xlarge"
      }
    }

    instances_distribution {
      // as of now, there is no support of usual if/else blocks
      // if var.linux_linux_spot_price == 0 use only on-demand instances, else will be used only on-spot
      on_demand_percentage_above_base_capacity = var.linux_spot_price == "" ? 100 : 0
      spot_max_price                           = var.linux_spot_price
      spot_allocation_strategy                 = "capacity-optimized-prioritized"
      on_demand_allocation_strategy            = "prioritized"
    }
  }

  desired_capacity = 0
  min_size         = 0
  max_size         = 50

  default_cooldown = 10

  health_check_type         = "EC2"
  health_check_grace_period = 10

  vpc_zone_identifier = [for s in aws_subnet.private_per_zone : s.id]

  termination_policies  = ["AllocationStrategy"]
  protect_from_scale_in = true

  force_delete            = true
  service_linked_role_arn = format("arn:aws:iam::%s:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling", data.aws_caller_identity.current.account_id)
}

resource "aws_autoscaling_group" "windows" {
  name = local.e3s_windows_autoscaling_name
  mixed_instances_policy {
    launch_template {
      launch_template_specification {
        launch_template_id = aws_launch_template.e3s_windows.id
        version            = aws_launch_template.e3s_windows.latest_version
      }

      override {
        weighted_capacity = 1
        instance_type     = "c5a.2xlarge"
      }

      override {
        weighted_capacity = 2
        instance_type     = "c5a.4xlarge"
      }
    }

    instances_distribution {
      // as of now, there is no support of usual if/else blocks
      // if var.windows_spot_price == 0 use only on-demand instances, else will be used only on-spot
      on_demand_percentage_above_base_capacity = var.windows_spot_price == "" ? 100 : 0
      spot_max_price                           = var.windows_spot_price
      spot_allocation_strategy                 = "capacity-optimized-prioritized"
      on_demand_allocation_strategy            = "prioritized"
    }
  }

  desired_capacity = 0
  min_size         = 0
  max_size         = 50

  default_cooldown = 10

  health_check_type         = "EC2"
  health_check_grace_period = 10

  vpc_zone_identifier = [for s in aws_subnet.private_per_zone : s.id]

  termination_policies  = ["AllocationStrategy"]
  protect_from_scale_in = true

  force_delete            = true
  service_linked_role_arn = format("arn:aws:iam::%s:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling", data.aws_caller_identity.current.account_id)
}

resource "aws_autoscaling_policy" "linux_forecast" {
  autoscaling_group_name = aws_autoscaling_group.linux.name
  name                   = "predictive"
  policy_type            = "PredictiveScaling"
  predictive_scaling_configuration {
    metric_specification {
      target_value = 100
      predefined_metric_pair_specification {
        predefined_metric_type = "ASGCPUUtilization"
      }
    }
    mode                         = "ForecastAndScale"
    scheduling_buffer_time       = "120"
    max_capacity_breach_behavior = "HonorMaxCapacity"
  }
}

resource "aws_autoscaling_policy" "windows_forecast" {
  autoscaling_group_name = aws_autoscaling_group.windows.name
  name                   = "predictive"
  policy_type            = "PredictiveScaling"
  predictive_scaling_configuration {
    metric_specification {
      target_value = 100
      predefined_metric_pair_specification {
        predefined_metric_type = "ASGCPUUtilization"
      }
    }
    mode                         = "ForecastAndScale"
    scheduling_buffer_time       = "300"
    max_capacity_breach_behavior = "HonorMaxCapacity"
  }
}


resource "aws_autoscaling_policy" "linux_cp_reservation" {
  autoscaling_group_name = aws_autoscaling_group.linux.name
  name                   = format("%s-%s", "ECSManagedAutoScalingPolicy", random_uuid.linux_policy.result)
  policy_type            = "TargetTrackingScaling"
  target_tracking_configuration {
    customized_metric_specification {
      metric_name = "CapacityProviderReservation"
      namespace   = "AWS/ECS/ManagedScaling"
      metric_dimension {
        name  = "CapacityProviderName"
        value = aws_ecs_capacity_provider.e3s_linux.name
      }
      metric_dimension {
        name  = "ClusterName"
        value = aws_ecs_cluster.e3s.name
      }
      statistic = "Average"
    }
    target_value     = 100
    disable_scale_in = false
  }
  enabled = false
}

resource "aws_autoscaling_policy" "windows_cp_reservation" {
  autoscaling_group_name = aws_autoscaling_group.windows.name
  name                   = format("%s-%s", "ECSManagedAutoScalingPolicy", random_uuid.windows_policy.result)
  policy_type            = "TargetTrackingScaling"
  target_tracking_configuration {
    customized_metric_specification {
      metric_name = "CapacityProviderReservation"
      namespace   = "AWS/ECS/ManagedScaling"
      metric_dimension {
        name  = "CapacityProviderName"
        value = aws_ecs_capacity_provider.e3s_windows.name
      }
      metric_dimension {
        name  = "ClusterName"
        value = aws_ecs_cluster.e3s.name
      }
      statistic = "Average"
    }
    target_value     = 100
    disable_scale_in = false
  }
  enabled = false
}

resource "random_uuid" "linux_policy" {
}

resource "random_uuid" "windows_policy" {
}