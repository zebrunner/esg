resource "aws_s3_bucket" "main" {
  count         = var.bucket.exists ? 0 : 1
  bucket        = var.bucket.name
  force_destroy = true
}
