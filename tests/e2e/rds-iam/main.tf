# Configuration used by rds_iam.sh: the provider itself logs in with AWS RDS IAM
# authentication, with the credentials of an assumed role.

variable "pg_host" {
  type = string
}

variable "pg_port" {
  type    = number
  default = 5432
}

variable "pg_user" {
  description = "Role of the database which is a member of rds_iam"
  type        = string
}

variable "iam_region" {
  type    = string
  default = ""
}

variable "iam_profile" {
  type    = string
  default = ""
}

variable "iam_role_arn" {
  type    = string
  default = ""
}

provider "postgresql" {
  host      = var.pg_host
  port      = var.pg_port
  username  = var.pg_user
  sslmode   = "require"
  superuser = false

  aws_rds_iam_auth              = true
  aws_rds_iam_region            = var.iam_region
  aws_rds_iam_profile           = var.iam_profile
  aws_rds_iam_provider_role_arn = var.iam_role_arn
}

resource "postgresql_role" "probe" {
  name = "tfpg_iam_probe"
}
