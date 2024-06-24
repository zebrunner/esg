variable "environment" {
  type     = string
  default  = "prod"
  nullable = false
}

variable "region" {
  type     = string
  default  = "us-east-1"
  nullable = false
}

variable "service_name" {
  type     = string
  default  = "e3s"
  nullable = false
}

variable "bucket_name" {
  type     = string
  default  = "engine"
  nullable = false
}

variable "zbr_host" {
  type    = string
  default = ""
}

variable "zbr_pass" {
  type    = string
  default = ""
}

variable "zbr_user" {
  type    = string
  default = ""
}

variable "key_name" {
  type    = string
  default = ""
}

variable "linux_spot_price" {
  type    = string
  default = ""
}

variable "windows_spot_price" {
  type    = string
  default = ""
}

# TODO: to delete
variable "linux_ami" {
  type    = string
  default = ""
}

variable "windows_ami" {
  type    = string
  default = ""
}

variable "cert" {
  type    = string
  default = ""
}

locals {
  e3s_server_instance_name = join("-", [var.service_name, var.environment])

  e3s_policy_name       = join("-", [var.service_name, var.environment, "policy"])
  e3s_role_name         = join("-", [var.service_name, var.environment, "role"])
  e3s_agent_policy_name = join("-", [var.service_name, var.environment, "agent", "policy"])
  e3s_agent_role_name   = join("-", [var.service_name, var.environment, "agent", "role"])
  e3s_task_policy_name  = join("-", [var.service_name, var.environment, "task", "policy"])
  e3s_task_role_name    = join("-", [var.service_name, var.environment, "task", "role"])

  e3s_bucket_name = format("zebrunner.%s-%s", var.environment, var.bucket_name)

  e3s_server_sg_name = join("-", [var.service_name, var.environment, "sg"])
  e3s_agent_sg_name  = join("-", [var.service_name, var.environment, "agent", "sg"])

  e3s_cluster_name                 = join("-", [var.service_name, var.environment])
  e3s_linux_launch_template_name   = join("-", [var.service_name, var.environment, "linux", "launch", "template"])
  e3s_windows_launch_template_name = join("-", [var.service_name, var.environment, "windows", "launch", "template"])
  e3s_linux_autoscaling_name       = join("-", [var.service_name, var.environment, "linux", "asg"])
  e3s_windows_autoscaling_name     = join("-", [var.service_name, var.environment, "windows", "asg"])
  e3s_linux_capacityprovider       = join("-", [var.service_name, var.environment, "linux", "capacityprovider"])
  e3s_windows_capacityprovider     = join("-", [var.service_name, var.environment, "windows", "capacityprovider"])
  e3s_tg_name                      = join("-", [var.service_name, var.environment, "tg"])
  e3s_alb_name                     = join("-", [var.service_name, var.environment, "alb"])
  e3s_listener_name                = join("-", [var.service_name, var.environment, "listener"])
}
