locals {
  zone_subnet_map = { for subnet in aws_subnet.public_per_zone : subnet.availability_zone => subnet.id }
}

resource "random_shuffle" "e3s_subnet_location" {
  input        = [for location in data.aws_ec2_instance_type_offerings.supported_server_zones.locations : location]
  result_count = 1
}

data "aws_ec2_instance_type_offerings" "supported_server_zones" {
  filter {
    name   = "instance-type"
    values = ["m5n.large"]
  }

  location_type = "availability-zone"
}

resource "tls_private_key" "pri_key" {
  count     = var.agent_ssh ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "aws_key_pair" "agent" {
  count      = var.agent_ssh ? 1 : 0
  key_name   = local.e3s_agent_key_name
  public_key = tls_private_key.pri_key[0].public_key_openssh
}

data "aws_ami" "ubuntu_22_04" {
  most_recent = true

  # Amazon
  owners = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "image-type"
    values = ["machine"]
  }

  filter {
    name   = "root-device-type"
    values = ["ebs"]
  }
}

resource "aws_instance" "e3s_server" {
  ami           = data.aws_ami.ubuntu_22_04.id
  instance_type = "m5n.large"

  subnet_id = local.zone_subnet_map[random_shuffle.e3s_subnet_location.result[0]]

  depends_on = [aws_ecs_cluster.e3s, aws_lb_listener.main]

  key_name = var.e3s_key_name

  vpc_security_group_ids = [aws_security_group.e3s_server.id]

  iam_instance_profile = aws_iam_instance_profile.e3s.name

  cpu_options {
    core_count       = 1
    threads_per_core = 2
  }

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  tags = {
    Name = local.e3s_server_instance_name
  }
  user_data = templatefile("./ec2_data/e3s_user_data.sh", {
    region                   = var.region
    cluster_name             = aws_ecs_cluster.e3s.name
    task_role                = aws_iam_role.e3s_task.name
    zbr_host                 = var.zbr_host
    zbr_user                 = var.zbr_user
    zbr_pass                 = var.zbr_pass
    env                      = var.environment
    linux_capacityprovider   = aws_ecs_capacity_provider.e3s_linux.name
    windows_capacityprovider = aws_ecs_capacity_provider.e3s_windows.name
    target_group             = aws_lb_target_group.main.name
    bucket_name              = aws_s3_bucket.main.bucket
    agent_key                = length(tls_private_key.pri_key) > 0 ? tls_private_key.pri_key[0].private_key_pem : ""
    agent_key_name           = local.e3s_agent_key_name
  })

  user_data_replace_on_change = true
}
