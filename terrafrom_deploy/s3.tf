resource "aws_s3_bucket" "main" {
  count         = var.bucket.exists ? 0 : 1
  provider      = aws.dir_bucket
  bucket        = var.bucket.name
  force_destroy = true
}

resource "aws_vpc_endpoint" "s3_gw" {
  count  = var.region == var.bucket.region ? 1 : 0
  vpc_id = aws_vpc.main.id

  route_table_ids   = [aws_route_table.internet-private.id]
  service_name      = format("com.amazonaws.%s.s3", var.region)
  vpc_endpoint_type = "Gateway"
  policy = templatefile("./iam_data/s3-gw-endpoint-policy.json", {
    bucket_name = var.bucket.name
    region      = var.region
  })
}

resource "aws_s3control_multi_region_access_point" "s3_access_point" {
  provider = aws.dir_bucket
  count    = var.region == var.bucket.region ? 0 : 1
  details {
    name = local.e3s_s3_access_point_name

    region {
      bucket = aws_s3_bucket.main[0].id
    }
  }
}

resource "aws_s3control_multi_region_access_point_policy" "s3_access_point_policy" {
  count    = var.region == var.bucket.region ? 0 : 1
  details {
    name = local.e3s_s3_access_point_name
    policy = templatefile("./iam_data/s3-access-point-policy.json", {
      account               = data.aws_caller_identity.current.account_id
      s3_access_point_alias = aws_s3control_multi_region_access_point.s3_access_point[0].alias
    })
  }
}

resource "aws_vpc_endpoint" "s3_interface" {
  count = var.region == var.bucket.region ? 0 : 1

  vpc_id     = aws_vpc.main.id
  subnet_ids = [for s in aws_subnet.private_per_zone : s.id]

  service_name        = "com.amazonaws.s3-global.accesspoint"
  vpc_endpoint_type   = "Interface"
  security_group_ids  = [aws_security_group.cloudwatch[0].id]
  private_dns_enabled = true
  dns_options {
    private_dns_only_for_inbound_resolver_endpoint = false
  }

  policy = templatefile("./iam_data/s3-interface-endpoint-policy.json", {
    account               = data.aws_caller_identity.current.account_id
    s3_access_point_alias = aws_s3control_multi_region_access_point.s3_access_point[0].alias
    bucket_name           = var.bucket.name
    region                = var.region
  })
}

resource "aws_s3_bucket_policy" "vpc_bucket_restrict_policy" {
  count    = length(aws_s3_bucket.main) > 0 ? 1 : 0
  provider = aws.dir_bucket
  bucket   = aws_s3_bucket.main[0].id
  policy = templatefile("./iam_data/s3-bucket-policy.json", {
    bucket_name     = aws_s3_bucket.main[0].id
    vpc_endpoint_id = length(aws_vpc_endpoint.s3_gw) > 0 ? aws_vpc_endpoint.s3_gw[0].id : aws_vpc_endpoint.s3_interface[0].id
  })
}
