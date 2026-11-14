# Configuration used by azure.sh: the provider logs in to an Azure Database for
# PostgreSQL flexible server with a token of Microsoft Entra (azure_identity_auth).

variable "pg_host" {
  type = string
}

variable "pg_user" {
  description = "Role of the database of the identity of Microsoft Entra"
  type        = string
}

variable "tenant_id" {
  type = string
}

provider "postgresql" {
  host      = var.pg_host
  port      = 5432
  username  = var.pg_user
  sslmode   = "require"
  superuser = false

  azure_identity_auth = true
  azure_tenant_id     = var.tenant_id
}

resource "postgresql_role" "probe" {
  name = "tfpg_azure_probe"
}
