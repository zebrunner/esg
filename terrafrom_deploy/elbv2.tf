resource "aws_lb_target_group" "main" {
  name             = local.e3s_tg_name
  vpc_id           = aws_vpc.main.id
  protocol         = "HTTP"
  protocol_version = "HTTP1"
  port             = 4444
  target_type      = "instance"

  health_check {
    protocol            = "HTTP"
    port                = "traffic-port"
    enabled             = "true"
    path                = "/"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 5
    unhealthy_threshold = 5
    matcher             = 200
  }

  deregistration_delay = 660
}

resource "aws_lb" "main" {
  name               = local.e3s_alb_name
  subnets            = [for s in aws_subnet.per_zones : s.id]
  security_groups    = [aws_security_group.e3s_server.id]
  load_balancer_type = "application"
  ip_address_type    = "ipv4"
  internal           = false
  idle_timeout       = 660
}

resource "aws_lb_listener" "main" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-2016-08"

  # TODO: create certificate? or parametrize? or both?
  certificate_arn = ""

  default_action {
    type  = "forward"
    order = 1
    forward {
      target_group {
        arn    = aws_lb_target_group.main.arn
        weight = 1
      }
      stickiness {
        enabled  = false
        duration = 3600
      }
    }
  }
}
