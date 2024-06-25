resource "aws_s3_bucket" "main" {
  count         = var.bucket_exists ? 0 : 1
  bucket        = var.bucket_name
  force_destroy = true
}
