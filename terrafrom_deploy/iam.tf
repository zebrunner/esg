data "aws_caller_identity" "current" {}

resource "aws_iam_policy" "e3s" {
  name = local.e3s_policy_name
  policy = templatefile("./iam_data/e3s-policy.json", {
    bucket_name = var.bucket.name
    env         = var.environment
    account     = data.aws_caller_identity.current.account_id
    region      = var.region
  })
}

resource "aws_iam_policy" "e3s_agent" {
  name = local.e3s_agent_policy_name
  policy = templatefile("./iam_data/e3s-agent-policy.json", {
    env    = var.environment
    accout = data.aws_caller_identity.current.account_id
    region = var.region
  })
}

resource "aws_iam_policy" "e3s_task" {
  name = local.e3s_task_policy_name
  policy = templatefile("./iam_data/e3s-task-policy.json", {
    bucket_name = var.bucket.name
  })
}

data "aws_iam_policy_document" "ecs_assume_role_policy" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "instance_assume_role_policy" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "e3s_task" {
  name                = local.e3s_task_role_name
  managed_policy_arns = [aws_iam_policy.e3s_task.arn]
  assume_role_policy  = data.aws_iam_policy_document.ecs_assume_role_policy.json
}

resource "aws_iam_role" "e3s" {
  name                = local.e3s_role_name
  managed_policy_arns = [aws_iam_policy.e3s.arn]
  assume_role_policy  = data.aws_iam_policy_document.instance_assume_role_policy.json
}

resource "aws_iam_role" "e3s_agent" {
  name                = local.e3s_agent_role_name
  managed_policy_arns = [aws_iam_policy.e3s_agent.arn]
  assume_role_policy  = data.aws_iam_policy_document.instance_assume_role_policy.json
}

resource "aws_iam_instance_profile" "e3s" {
  name = local.e3s_role_name
  role = aws_iam_role.e3s.name
}

resource "aws_iam_instance_profile" "e3s_agent" {
  name = local.e3s_agent_role_name
  role = aws_iam_role.e3s_agent.name
}
