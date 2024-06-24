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

  service_linked_role_arn = format("arn:aws:iam::%s:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling", data.aws_caller_identity.current.account_id)
}

# TODO: implement forecast enabling
