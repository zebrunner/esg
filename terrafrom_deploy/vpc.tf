resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  instance_tenancy     = "default"
  enable_dns_support   = "true"
  enable_dns_hostnames = "true"
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_subnet" "public_per_zone" {
  for_each = { for id, az_name in data.aws_availability_zones.available.names : id => az_name }

  vpc_id                  = aws_vpc.main.id
  map_public_ip_on_launch = true
  availability_zone       = each.value
  cidr_block              = cidrsubnet(aws_vpc.main.cidr_block, 8, each.key)
}

resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id
}

resource "aws_route_table" "internet" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }
}

resource "aws_route_table_association" "internet-associations" {
  for_each       = (tomap(aws_subnet.public_per_zone))
  route_table_id = aws_route_table.internet.id
  subnet_id      = aws_subnet.public_per_zone[each.key].id
}

resource "random_shuffle" "public_subnet" {
  input        = [for subnet in aws_subnet.public_per_zone : subnet.id]
  result_count = 1
}

resource "aws_subnet" "private_per_zone" {
  for_each = { for id, az_name in data.aws_availability_zones.available.names : id => az_name }

  vpc_id                  = aws_vpc.main.id
  map_public_ip_on_launch = false
  availability_zone       = each.value
  cidr_block              = cidrsubnet(aws_vpc.main.cidr_block, 8, length(aws_subnet.public_per_zone) + each.key)
}

resource "aws_route_table" "internet-private" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.nat-gw.id
  }
}

resource "aws_route_table_association" "nat-associations" {
  for_each       = (tomap(aws_subnet.private_per_zone))
  route_table_id = aws_route_table.internet-private.id
  subnet_id      = aws_subnet.private_per_zone[each.key].id
}

resource "aws_eip" "nat" {
  domain               = "vpc"
  network_border_group = var.region
}

resource "aws_nat_gateway" "nat-gw" {
  allocation_id = aws_eip.nat.id
  subnet_id     = random_shuffle.public_subnet.result[0]

  depends_on = [aws_internet_gateway.igw]
}
