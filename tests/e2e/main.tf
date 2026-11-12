# Configuration used by run.sh to test the role / database parameters
# end to end, with the Terraform CLI.

terraform {
  required_providers {
    postgresql = {
      source = "cyrilgdn/postgresql"
    }
  }
}

variable "pg_host" {
  type = string
}

variable "pg_port" {
  type    = number
  default = 5432
}

variable "pg_user" {
  type = string
}

variable "pg_password" {
  type      = string
  sensitive = true
}

variable "pg_sslmode" {
  type    = string
  default = "require"
}

variable "audit_enabled" {
  description = "Enable pgaudit for the application role and its database"
  type        = bool
}

variable "audit_classes" {
  description = "Classes of statements logged by pgaudit when the audit is enabled"
  type        = string
  default     = "ddl, write"
}

variable "role_parameters" {
  description = "Other configuration parameters of the application role"
  type        = map(string)
  default     = {}
}

variable "database_parameters" {
  description = "Other configuration parameters of the application database"
  type        = map(string)
  default     = {}
}

provider "postgresql" {
  host      = var.pg_host
  port      = var.pg_port
  username  = var.pg_user
  password  = var.pg_password
  sslmode   = var.pg_sslmode
  superuser = false
}

locals {
  pgaudit_log = var.audit_enabled ? var.audit_classes : "none"
}

resource "postgresql_role" "app" {
  name  = "e2e_app"
  login = true

  parameters = merge(var.role_parameters, {
    "pgaudit.log" = local.pgaudit_log
  })
}

resource "postgresql_database" "app" {
  name  = "e2e_app"
  owner = postgresql_role.app.name

  parameters = merge(var.database_parameters, {
    "pgaudit.log" = local.pgaudit_log
  })
}
