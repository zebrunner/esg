resource "aws_s3_bucket" "main" {
  count         = var.bucket.exists ? 0 : 1
  bucket        = var.bucket.name
  force_destroy = true
}

resource "aws_s3_bucket_policy" "vpc_restrict_policy" {
  count  = var.bucket.exists ? 0 : 1
  bucket = aws_s3_bucket.main[0].id
  policy = templatefile("./iam_data/s3-bucket-policy.json", {
    bucket_name     = var.bucket.name
    vpc_endpoint_id = aws_vpc_endpoint.s3_gw.id
  })
}

resource "aws_vpc_endpoint" "s3_gw" {
  vpc_id = aws_vpc.main.id

  route_table_ids   = [aws_route_table.internet-private.id]
  service_name      = format("com.amazonaws.%s.s3", var.region)
  vpc_endpoint_type = "Gateway"
  policy = templatefile("./iam_data/s3-endpoint-policy.json", {
    bucket_name = var.bucket.name
    region      = var.region
  })
}
