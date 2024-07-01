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

output "db_dns" {
  description = "aurora dns"
  value       = aws_rds_cluster.aurora.endpoint
}

output "cache_address" {
  description = "redis read/write host:port"
  value       = format("%s:%s", aws_elasticache_serverless_cache.redis.endpoint[0].address, aws_elasticache_serverless_cache.redis.endpoint[0].port)
}
