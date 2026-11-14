# Configuration used by gcp.sh: the provider connects to a Google Cloud SQL instance
# through the Cloud SQL proxy (scheme = "gcppostgres"), with a password or with the
# IAM database authentication of an impersonated service account.

variable "host" {
  description = "The instance, as project:region:instance or project/region/instance"
  type        = string
}

variable "pg_user" {
  type = string
}

variable "pg_password" {
  type      = string
  default   = ""
  sensitive = true
}

variable "impersonate" {
  description = "Service account to impersonate (IAM database authentication)"
  type        = string
  default     = ""
}

provider "postgresql" {
  scheme    = "gcppostgres"
  host      = var.host
  username  = var.pg_user
  password  = var.pg_password
  superuser = false

  gcp_iam_impersonate_service_account = var.impersonate
}

resource "postgresql_role" "probe" {
  name = "tfpg_gcp_probe"
}
