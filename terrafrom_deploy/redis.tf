locals {
  subnets = sort([for subnet in aws_subnet.private_per_zone : subnet.id])
}

resource "aws_elasticache_serverless_cache" "redis" {
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

  subnet_ids         = [local.subnets[0],local.subnets[1]]
  security_group_ids = [aws_security_group.redis.id]
}

