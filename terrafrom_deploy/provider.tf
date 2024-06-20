terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "5.54.1"
    }
  }

  required_version = "~> 1.8.5"
}

# configure permissons
provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Environment = var.environment
      Name        = var.service_name
    }
  }
}
