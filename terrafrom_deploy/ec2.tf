data "aws_ami" "zbr_linux" {
  most_recent = true
  owners      = ["aws-marketplace"]
  filter {
    name   = "name"
    values = ["Zebrunner ESG Agent *"]
  }

  filter {
    name   = "block-device-mapping.device-name"
    values = ["/dev/xvda"]
  }
}

data "aws_ami" "zbr_windows" {
  most_recent = true
  owners      = ["aws-marketplace"]
  filter {
    name   = "name"
    values = ["Zebrunner ESG Agent *"]
  }

  filter {
    name   = "platform"
    values = ["windows"]
  }
}

resource "aws_launch_template" "e3s_linux" {
  name                   = local.e3s_linux_launch_template_name
  image_id               = data.aws_ami.zbr_linux.id
  vpc_security_group_ids = [aws_security_group.e3s_agent.id]
  ebs_optimized          = true
  key_name               = var.allow_agent_ssh ? aws_key_pair.agent[0].key_name : ""

  instance_initiated_shutdown_behavior = "terminate"

  block_device_mappings {
    device_name = "/dev/xvda"
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
    name = aws_iam_instance_profile.e3s_agent.name
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

  user_data = base64encode(templatefile("./ec2_data/linux_user_data.sh", { cluster_name = local.e3s_cluster_name, cidr_block = aws_vpc.main.cidr_block }))

  depends_on = [aws_iam_instance_profile.e3s_agent]
}

resource "aws_launch_template" "e3s_windows" {
  name                   = local.e3s_windows_launch_template_name
  image_id               = data.aws_ami.zbr_windows.id
  vpc_security_group_ids = var.allow_agent_ssh ? [aws_security_group.e3s_agent.id, aws_security_group.windows_rdp[0].id] : [aws_security_group.e3s_agent.id]
  key_name               = var.allow_agent_ssh ? aws_key_pair.agent[0].key_name : ""

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
    name = aws_iam_instance_profile.e3s_agent.name
  }

  user_data = base64encode(templatefile("./ec2_data/windows_user_data.ps1", { cluster_name = local.e3s_cluster_name, cidr_block = aws_vpc.main.cidr_block }))

  depends_on = [aws_iam_instance_profile.e3s_agent]
}
