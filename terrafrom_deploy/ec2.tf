resource "aws_launch_template" "e3s_linux" {
  name = local.e3s_linux_launch_template_name
  # TODO: Parametrize ami id recieve by region
  image_id               = ""
  vpc_security_group_ids = [aws_security_group.e3s_agent.id]
  ebs_optimized          = true

  instance_initiated_shutdown_behavior = "terminate"

  block_device_mappings {
    device_name = "/dev/xvdcz"
    ebs {
      volume_size           = 70
      volume_type           = "gp3"
      delete_on_termination = true
      encrypted             = true
    }
  }

  monitoring {
    enabled = true
  }

  iam_instance_profile {
    arn = aws_iam_instance_profile.e3s_agent.arn
  }

  hibernation_options {
    configured = false
  }

  enclave_options {
    enabled = false
  }

  metadata_options {
    http_tokens                 = "required"
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 1
  }

  disable_api_termination = false

  user_data = base64encode(templatefile("./template_data/linux_user_data.sh", { cluster_name = local.e3s_cluster_name }))
}

resource "aws_launch_template" "e3s_windows" {
  name                   = local.e3s_windows_launch_template_name
  image_id               = ""
  vpc_security_group_ids = [aws_security_group.e3s_agent.id]
  # TODO: should we create/append key?
  # key_name = 
  ebs_optimized = true
  block_device_mappings {
    device_name = "/dev/sda1"
    ebs {
      volume_size           = 100
      volume_type           = "gp3"
      delete_on_termination = true
      encrypted             = true
    }
  }
  monitoring {
    enabled = true
  }
  disable_api_termination = false

  instance_initiated_shutdown_behavior = "terminate"
  hibernation_options {
    configured = false
  }
  metadata_options {
    http_tokens                 = "required"
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 1
  }
  enclave_options {
    enabled = false
  }

  iam_instance_profile {
    arn = aws_iam_instance_profile.e3s_agent.arn
  }

  user_data = base64encode(templatefile("./template_data/windows_user_data.ps1", { cluster_name = local.e3s_cluster_name, cidr_block = aws_vpc.main.cidr_block }))
}
