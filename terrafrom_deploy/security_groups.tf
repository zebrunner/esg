resource "aws_security_group" "e3s_server" {
  vpc_id = aws_vpc.main.id
  name   = local.e3s_server_sg_name
}

resource "aws_vpc_security_group_ingress_rule" "e3s_server_alb" {
  security_group_id = aws_security_group.e3s_server.id
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
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

# TODO: delete
resource "aws_vpc_security_group_ingress_rule" "e3s_agent_ssh_ipv4" {
  count             = var.agent_ssh ? 1 : 0
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
