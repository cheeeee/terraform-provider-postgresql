terraform {
  # Ephemeral variables and write-only attributes (the passwords are never stored
  # in the state) need Terraform >= 1.11 or OpenTofu >= 1.11.
  required_version = ">= 1.11"

  required_providers {
    postgresql = {
      source = "cheeeee/postgresql"
    }
  }
}
