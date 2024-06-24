terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "5.54.1"
    }

    random = {
      source  = "hashicorp/random"
      version = "3.6.2"
    }
  }

  required_version = "~> 1.8.5"
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Environment = var.environment
      Name        = var.service_name
    }
  }
}
