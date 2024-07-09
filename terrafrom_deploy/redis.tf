locals {
  az_names = sort([for az_name in data.aws_availability_zones.available.names : az_name])
}

# restrict subnets to first 2 in lexicographical order regions (example: us-east-1a, us-east-1b) 
data "aws_subnets" "redis" {
  count = var.data_layer_remote ? 1 : 0
  filter {
    name   = "vpc-id"
    values = [aws_vpc.main.id]
  }

  filter {
    name   = "availability-zone"
    values = [local.az_names[0], local.az_names[1]]
  }

  filter {
    name   = "subnet-id"
    values = [for subnet in aws_subnet.private_per_zone : subnet.id]
  }

  depends_on = [aws_subnet.private_per_zone]
}

resource "aws_elasticache_serverless_cache" "redis" {
  count                = var.data_layer_remote ? 1 : 0
  name                 = local.e3s_serverless_cache_name
  engine               = "redis"
  major_engine_version = "7"

  cache_usage_limits {
    data_storage {
      maximum = 5
      unit    = "GB"
    }
    ecpu_per_second {
      maximum = 5000
    }
  }

  subnet_ids         = data.aws_subnets.redis[0].ids
  security_group_ids = [aws_security_group.redis[0].id]
}

