## TODO: reuse existing one instead of creating?
resource "aws_s3_bucket" "main" {
  bucket_prefix = local.e3s_bucket_name
  force_destroy = true
}
