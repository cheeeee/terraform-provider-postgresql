variable "name" {
  description = "Name of the database, also used as the prefix of the roles (<name>_owner, <name>_migrator, <name>_rw)."
  type        = string

  validation {
    condition     = can(regex("^[a-z_][a-z0-9_]*$", var.name))
    error_message = "The name must be a lowercase identifier (letters, digits and underscores)."
  }
}

variable "roles" {
  description = <<-EOT
    Other roles of the database, by name:
      access  "ro" (default) to read the data, "rw" to read and write it
      audit   value of pgaudit.log for the sessions of this role, it overrides the
              audit of the database (e.g. "none" for a role which must not be audited)
      iam     overrides iam_auth for this role
  EOT
  type = map(object({
    access = optional(string, "ro")
    audit  = optional(string)
    iam    = optional(bool)
  }))
  default = {}

  validation {
    condition     = alltrue([for role in values(var.roles) : contains(["ro", "rw"], role.access)])
    error_message = "The access of a role is \"ro\" or \"rw\"."
  }
}

variable "iam_auth" {
  description = "On AWS RDS, let the roles log in with an IAM token (membership of rds_iam) instead of a password."
  type        = bool
  default     = false
}

variable "passwords" {
  description = "Passwords of the roles which do not use IAM, by name of role (e.g. read from a secrets manager). They are write-only: never stored in the plan or in the state."
  type        = map(string)
  default     = {}
  sensitive   = true
  ephemeral   = true
}

variable "password_versions" {
  description = "Version of the password of the roles, by name of role. A role only gets a password if it has a version, and a new password is only applied when its version changes."
  type        = map(string)
  default     = {}
}

variable "audit" {
  description = "Classes of statements logged by pgaudit for every session of the database (e.g. \"ddl, write\", \"all\", \"none\"). null to not set pgaudit.log. pgaudit has to be loaded on the server."
  type        = string
  default     = null
}

variable "database_parameters" {
  description = "Other configuration parameters of the database (ALTER DATABASE ... SET), e.g. statement_timeout. They are the only parameters of the database: the ones set outside of Terraform are removed."
  type        = map(string)
  default     = {}
}

variable "extensions" {
  description = "Extensions to create in the database: the application cannot create the ones which are not trusted."
  type        = list(string)
  default     = []
}

variable "connection_limit" {
  description = "Maximum number of concurrent connections to the database, -1 for no limit."
  type        = number
  default     = -1
}
