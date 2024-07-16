locals {
  zone_subnet_map = { for subnet in aws_subnet.public_per_zone : subnet.availability_zone => subnet.id }
}

resource "random_shuffle" "e3s_subnet_location" {
  input        = sort([for location in data.aws_ec2_instance_type_offerings.supported_server_zones.locations : location])
  result_count = 1
}

data "aws_ec2_instance_type_offerings" "supported_server_zones" {
  filter {
    name   = "instance-type"
    values = [var.e3s_server_instance_type]
  }

  location_type = "availability-zone"
}

resource "tls_private_key" "pri_key" {
  count     = var.allow_agent_ssh ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "aws_key_pair" "agent" {
  count      = var.allow_agent_ssh ? 1 : 0
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
  instance_type = var.e3s_server_instance_type

  subnet_id = local.zone_subnet_map[random_shuffle.e3s_subnet_location.result[0]]

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
    cluster_name             = local.e3s_cluster_name
    task_role                = local.e3s_task_role_name
    env                      = var.environment
    linux_capacityprovider   = local.e3s_linux_capacityprovider
    windows_capacityprovider = local.e3s_windows_capacityprovider
    target_group             = local.e3s_tg_name
    bucket_name              = length(aws_s3control_multi_region_access_point.s3_access_point) > 0 ? aws_s3control_multi_region_access_point.s3_access_point[0].arn : var.bucket.name
    bucket_region            = var.bucket.region
    log_group                = length(aws_cloudwatch_log_group.e3s_tasks) > 0 ? local.e3s_log_group_name : ""

    zbr_host = var.zebrunner.host
    zbr_user = var.zebrunner.user
    zbr_pass = var.zebrunner.pass

    agent_key      = length(tls_private_key.pri_key) > 0 ? tls_private_key.pri_key[0].private_key_pem : ""
    agent_key_name = local.e3s_agent_key_name

    # db_dns      = aws_rds_cluster.aurora.endpoint
    remote_data   = var.data_layer_remote
    db_username   = var.remote_db.username
    db_pass       = var.remote_db.pass
    db_name       = var.data_layer_remote ? aws_db_instance.postgres[0].db_name : ""
    db_dns        = var.data_layer_remote ? aws_db_instance.postgres[0].endpoint : ""
    cache_address = var.data_layer_remote ? aws_elasticache_serverless_cache.redis[0].endpoint[0].address : ""
    cache_port    = var.data_layer_remote ? aws_elasticache_serverless_cache.redis[0].endpoint[0].port : ""
  })

  # depends_on = [aws_ecs_cluster.e3s, aws_lb_listener.main, aws_rds_cluster_instance.aurora_instance]
  depends_on = [aws_ecs_cluster.e3s, aws_lb_listener.main]

  lifecycle {
    ignore_changes = [user_data]
  }
}
