# Configuration used by rds_secret.sh on AWS RDS: the roles of the application
# log in with an IAM token, and one role which is not audited gets its password
# from AWS Secrets Manager.

terraform {
  required_providers {
    postgresql = {
      source = "cheeeee/postgresql"
    }
    aws = {
      source = "hashicorp/aws"
    }
  }
}

variable "pg_host" {
  type = string
}

variable "pg_user" {
  type = string
}

variable "pg_password" {
  type      = string
  sensitive = true
}

variable "aws_region" {
  type = string
}

variable "secret_id" {
  description = "Secret of AWS Secrets Manager which contains the password of the reporting role."
  type        = string
}

variable "password_version" {
  type    = string
  default = "1"
}

provider "aws" {
  region = var.aws_region
}

provider "postgresql" {
  host      = var.pg_host
  port      = 5432
  username  = var.pg_user
  password  = var.pg_password
  sslmode   = "require"
  superuser = false
}

# The password is read at every run and never stored in the plan or in the state.
ephemeral "aws_secretsmanager_secret_version" "reporting" {
  secret_id = var.secret_id
}

module "orders" {
  source = "./app-database"

  name  = "e2e_iam"
  audit = "ddl, write"

  # The roles of the application have no password
  iam_auth = true

  roles = {
    # Not audited, and it logs in with the password of the secret
    e2e_iam_reporting = { audit = "none", iam = false }
  }

  passwords = {
    e2e_iam_reporting = ephemeral.aws_secretsmanager_secret_version.reporting.secret_string
  }
  password_versions = {
    e2e_iam_reporting = var.password_version
  }
}
