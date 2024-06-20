resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  instance_tenancy     = "default"
  enable_dns_support   = "true"
  enable_dns_hostnames = "true"
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_subnet" "per_zones" {
  for_each = { for id, az_name in data.aws_availability_zones.available.names : id => az_name }

  vpc_id                  = aws_vpc.main.id
  map_public_ip_on_launch = false
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

resource "aws_route_table_association" "associations" {
  for_each       = (tomap(aws_subnet.per_zones))
  route_table_id = aws_route_table.internet.id
  subnet_id      = aws_subnet.per_zones[each.key].id
}
