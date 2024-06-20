
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
      // TODO: shold be availiable two variants:
      // only on-spot or only on-demand
      on_demand_allocation_strategy            = "prioritized"
      on_demand_percentage_above_base_capacity = 100
    }
  }

  desired_capacity = 0
  min_size         = 0
  max_size         = 50

  default_cooldown = 10

  health_check_type         = "EC2"
  health_check_grace_period = 10

  vpc_zone_identifier = [for s in aws_subnet.per_zones : s.id]

  termination_policies  = ["AllocationStrategy"]
  protect_from_scale_in = true

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
      on_demand_allocation_strategy            = "prioritized"
      on_demand_percentage_above_base_capacity = 100
    }
  }

  desired_capacity = 0
  min_size         = 0
  max_size         = 50

  default_cooldown = 10

  health_check_type         = "EC2"
  health_check_grace_period = 10

  vpc_zone_identifier = [for s in aws_subnet.per_zones : s.id]

  termination_policies  = ["AllocationStrategy"]
  protect_from_scale_in = true

  service_linked_role_arn = format("arn:aws:iam::%s:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling", data.aws_caller_identity.current.account_id)
}

# TODO: implement forecast enabling
