# Configuration used by run.sh to test the examples/app-database module.

terraform {
  required_providers {
    postgresql = {
      source = "cheeeee/postgresql"
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

variable "audit" {
  type = string
}

variable "roles" {
  type = map(object({
    access = optional(string, "ro")
    audit  = optional(string)
    iam    = optional(bool)
  }))
  default = {
    e2e_orders_ro      = {}
    e2e_orders_noaudit = { audit = "none" }
  }
}

# The passwords are never stored in the state
variable "passwords" {
  type      = map(string)
  sensitive = true
  ephemeral = true
}

variable "password_version" {
  type    = string
  default = "1"
}

provider "postgresql" {
  host      = var.pg_host
  port      = var.pg_port
  username  = var.pg_user
  password  = var.pg_password
  sslmode   = var.pg_sslmode
  superuser = false
}

module "orders" {
  source = "./app-database"

  name       = "e2e_orders"
  audit      = var.audit
  extensions = ["pgcrypto"]
  roles      = var.roles

  passwords = var.passwords
  password_versions = {
    for name in concat(["e2e_orders_migrator", "e2e_orders_rw"], keys(var.roles)) : name => var.password_version
  }

  database_parameters = {
    statement_timeout = "30s"
  }
}

output "orders" {
  value = module.orders
}
