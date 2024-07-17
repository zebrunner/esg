output "e3s_ip" {
  description = "public adress of e3s server"
  value       = aws_instance.e3s_server.public_ip
}

output "nat_gw_ip" {
  description = "adress of nat gateway"
  value       = aws_nat_gateway.nat-gw.public_ip
}

output "lb_dns" {
  description = "load balancer dns"
  value       = aws_lb.main.dns_name
}

output "vpc_id" {
  description = "new vpc"
  value       = aws_vpc.main.id
}

# output "db_dns" {
#   description = "aurora dns"
#   value       = aws_rds_cluster.aurora.endpoint
# }

output "db_dns" {
  description = "postgres dns"
  value       = length(aws_db_instance.postgres) != 0 ? aws_db_instance.postgres[0].endpoint : "remote db is not created"
}

output "cache_address" {
  description = "redis read/write host:port"
  value       = length(aws_elasticache_serverless_cache.redis) != 0 ? format("%s:%s", aws_elasticache_serverless_cache.redis[0].endpoint[0].address, aws_elasticache_serverless_cache.redis[0].endpoint[0].port) : "remote redis is not created"
}

output "cloudwatch_vpc_endpoint_id" {
  description = "vpc interface endpoint for cloudwatch logs upload"
  value       = length(aws_vpc_endpoint.cloudwatch) != 0 ? aws_vpc_endpoint.cloudwatch[0].id : "cloudwatch endpoint is not created"
}

output "s3_vpc_gw_endpoint_id" {
  description = "vpc gateway endpoint for s3 artifacts upload"
  value       = aws_vpc_endpoint.s3_gw.id
}
