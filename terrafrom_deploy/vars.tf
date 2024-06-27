# TODO: add custom condition for vars
variable "environment" {
  type     = string
  default  = "prod"
  nullable = false
}

variable "region" {
  type     = string
  nullable = false
}

variable "bucket" {
  type = object({
    exists = bool
    name   = string
    region = string
  })
  nullable = false
}

variable "e3s_key_name" {
  type     = string
  nullable = false
}

variable "agent_ssh" {
  type    = bool
  default = false
}

variable "instance_types" {
  type = list(object({
    weight        = number
    instance_type = string
  }))
  default = [
    {
      weight        = 1
      instance_type = "c5a.4xlarge"
    },
    {
      weight        = 2
      instance_type = "c5a.8xlarge"
    }
  ]
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

variable "linux_spot_price" {
  type    = string
  default = ""
}

variable "windows_spot_price" {
  type    = string
  default = ""
}

variable "cert" {
  type    = string
  default = ""
}

# consts
locals {
  service_name = "e3s"

  e3s_server_instance_name = join("-", [local.service_name, var.environment])

  e3s_agent_key_name = join("-", [local.service_name, var.environment, "agent"])

  e3s_policy_name       = join("-", [local.service_name, var.environment, "policy"])
  e3s_role_name         = join("-", [local.service_name, var.environment, "role"])
  e3s_agent_policy_name = join("-", [local.service_name, var.environment, "agent", "policy"])
  e3s_agent_role_name   = join("-", [local.service_name, var.environment, "agent", "role"])
  e3s_task_policy_name  = join("-", [local.service_name, var.environment, "task", "policy"])
  e3s_task_role_name    = join("-", [local.service_name, var.environment, "task", "role"])

  e3s_server_sg_name = join("-", [local.service_name, var.environment, "sg"])
  e3s_agent_sg_name  = join("-", [local.service_name, var.environment, "agent", "sg"])
  e3s_rdp_sg_name    = join("-", [local.service_name, var.environment, "rdp", "sg"])

  e3s_cluster_name                 = join("-", [local.service_name, var.environment])
  e3s_linux_launch_template_name   = join("-", [local.service_name, var.environment, "linux", "launch", "template"])
  e3s_windows_launch_template_name = join("-", [local.service_name, var.environment, "windows", "launch", "template"])
  e3s_linux_autoscaling_name       = join("-", [local.service_name, var.environment, "linux", "asg"])
  e3s_windows_autoscaling_name     = join("-", [local.service_name, var.environment, "windows", "asg"])
  e3s_linux_capacityprovider       = join("-", [local.service_name, var.environment, "linux", "capacityprovider"])
  e3s_windows_capacityprovider     = join("-", [local.service_name, var.environment, "windows", "capacityprovider"])
  e3s_tg_name                      = join("-", [local.service_name, var.environment, "tg"])
  e3s_alb_name                     = join("-", [local.service_name, var.environment, "alb"])
  e3s_listener_name                = join("-", [local.service_name, var.environment, "listener"])
}
