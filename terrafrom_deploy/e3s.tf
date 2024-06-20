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
  ami                         = data.aws_ami.ubuntu_22_04.id
  instance_type               = "m5n.large"
  subnet_id                   = aws_subnet.per_zones[0].id
  associate_public_ip_address = false

  # TODO: add key generation for ssh connection

  vpc_security_group_ids = [aws_security_group.e3s_server.id]

  iam_instance_profile = aws_iam_instance_profile.e3s.name

  # TODO: implement template (userdata)

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
}

data "cloudinit_config" "cloudinit-example" {
  gzip          = false
  base64_encode = false

  part {
    filename     = "init.cfg"
    content_type = "text/cloud-config"
    content      = templatefile("./ec2_data/init.cfg")
  }

  part {
    content_type = "text/x-shellscript"
    content = templatefile("./ec2_data/e3s_user_data.sh", {
      region                   = var.region
      cluster_name             = aws_ecs_cluster.e3s.name
      task_role                = aws_iam_role.e3s_task
      zbr_host                 = var.zbr_host
      zbr_user                 = var.zbr_user
      zbr_pass                 = var.zbr_pass
      env                      = var.environment
      linux_capacityprovider   = aws_ecs_capacity_provider.e3s_linux.name
      windows_capacityprovider = aws_ecs_capacity_provider.e3s_windows.name
      target_group             = aws_lb_target_group.main.name
      bucket_name              = aws_s3_bucket.main.bucket
    })
  }
}


