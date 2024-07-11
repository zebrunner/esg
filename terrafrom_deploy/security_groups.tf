resource "aws_security_group" "e3s_server" {
  vpc_id = aws_vpc.main.id
  name   = local.e3s_server_sg_name
}

resource "aws_vpc_security_group_ingress_rule" "e3s_server_alb" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = var.cert == "" ? 80 : 443
  to_port           = var.cert == "" ? 80 : 443
}

resource "aws_vpc_security_group_ingress_rule" "e3s_server_router_ports" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 4444
  to_port           = 4445
  description       = "router_ports"
}

resource "aws_vpc_security_group_ingress_rule" "e3s_server_ssh_ipv4" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 22
  to_port           = 22
  description       = "ssh"
}

resource "aws_vpc_security_group_ingress_rule" "e3s_server_ssh_ipv6" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "tcp"
  cidr_ipv6         = "::/0"
  from_port         = 22
  to_port           = 22
  description       = "ssh"
}

resource "aws_vpc_security_group_egress_rule" "e3s_server_outbound_trafic_ipv4" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "e3s_server_outbound_trafic_ipv6" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

resource "aws_security_group" "e3s_agent" {
  vpc_id = aws_vpc.main.id
  name   = local.e3s_agent_sg_name
}

resource "aws_vpc_security_group_ingress_rule" "e3s_agent_inbound_trafic" {
  security_group_id = aws_security_group.e3s_agent.id
  ip_protocol       = "tcp"
  cidr_ipv4         = "${aws_instance.e3s_server.private_ip}/32"
  description       = "docker port range to access from e3s server"
  from_port         = 32768
  to_port           = 64536
}

resource "aws_vpc_security_group_ingress_rule" "e3s_agent_ssh_ipv4" {
  count             = var.allow_agent_ssh ? 1 : 0
  security_group_id = aws_security_group.e3s_agent.id
  ip_protocol       = "tcp"
  cidr_ipv4         = "${aws_instance.e3s_server.private_ip}/32"
  from_port         = 22
  to_port           = 22
  description       = "ssh"
}

resource "aws_vpc_security_group_egress_rule" "e3s_agent_outbound_trafic_ipv4" {
  security_group_id = aws_security_group.e3s_agent.id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "e3s_agent_outbound_trafic_ipv6" {
  security_group_id = aws_security_group.e3s_agent.id
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

resource "aws_security_group" "windows_rdp" {
  count  = var.allow_agent_ssh ? 1 : 0
  vpc_id = aws_vpc.main.id
  name   = local.e3s_rdp_sg_name
}

resource "aws_vpc_security_group_ingress_rule" "e3s_rdp_ipv4" {
  count             = length(aws_security_group.windows_rdp)
  security_group_id = aws_security_group.windows_rdp[count.index].id
  ip_protocol       = "tcp"
  cidr_ipv4         = "${aws_instance.e3s_server.private_ip}/32"
  from_port         = 3389
  to_port           = 3389
}

resource "aws_vpc_security_group_egress_rule" "e3s_rdp_outbound_trafic_ipv4" {
  count             = length(aws_security_group.windows_rdp)
  security_group_id = aws_security_group.windows_rdp[count.index].id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "e3s_rdp_outbound_trafic_ipv6" {
  count             = length(aws_security_group.windows_rdp)
  security_group_id = aws_security_group.windows_rdp[count.index].id
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

resource "aws_security_group" "rds" {
  count  = var.data_layer_remote ? 1 : 0
  vpc_id = aws_vpc.main.id
  name   = local.e3s_rds_sg_name
}

resource "aws_vpc_security_group_egress_rule" "e3s_rds_outbound_trafic_ipv4" {
  count             = var.data_layer_remote ? 1 : 0
  security_group_id = aws_security_group.rds[0].id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "e3s_rds_outbound_trafic_ipv6" {
  count             = var.data_layer_remote ? 1 : 0
  security_group_id = aws_security_group.rds[0].id
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

resource "aws_vpc_security_group_ingress_rule" "e3s_rds_ipv4" {
  count             = var.data_layer_remote ? 1 : 0
  security_group_id = aws_security_group.rds[0].id
  ip_protocol       = "tcp"
  cidr_ipv4         = "${aws_instance.e3s_server.private_ip}/32"
  from_port         = 5432
  to_port           = 5432
}

resource "aws_security_group" "redis" {
  count  = var.data_layer_remote ? 1 : 0
  vpc_id = aws_vpc.main.id
  name   = local.e3s_cache_sg_name
}

resource "aws_vpc_security_group_egress_rule" "e3s_redis_outbound_trafic_ipv4" {
  count             = var.data_layer_remote ? 1 : 0
  security_group_id = aws_security_group.redis[0].id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "e3s_redis_outbound_trafic_ipv6" {
  count             = var.data_layer_remote ? 1 : 0
  security_group_id = aws_security_group.redis[0].id
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

resource "aws_vpc_security_group_ingress_rule" "e3s_redis_ipv4" {
  count             = var.data_layer_remote ? 1 : 0
  security_group_id = aws_security_group.redis[0].id
  ip_protocol       = "tcp"
  cidr_ipv4         = "${aws_instance.e3s_server.private_ip}/32"
  from_port         = 6379
  to_port           = 6380
}

resource "aws_security_group" "cloudwatch" {
  count  = var.enable_cloudwatch ? 1 : 0
  vpc_id = aws_vpc.main.id
  name   = local.e3s_cloudwatch_endpoint_sg_name
}

resource "aws_vpc_security_group_ingress_rule" "cloudwatch" {
  count                        = var.enable_cloudwatch ? 1 : 0
  security_group_id            = aws_security_group.cloudwatch[0].id
  ip_protocol                  = "-1"
  referenced_security_group_id = aws_security_group.e3s_agent.id
}

resource "aws_vpc_security_group_egress_rule" "cloudwatch_outbound_trafic_ipv4" {
  count             = var.enable_cloudwatch ? 1 : 0
  security_group_id = aws_security_group.cloudwatch[0].id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "cloudwatch_outbound_trafic_ipv6" {
  count             = var.enable_cloudwatch ? 1 : 0
  security_group_id = aws_security_group.cloudwatch[0].id
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}
