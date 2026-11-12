output "database" {
  description = "Name of the database."
  value       = postgresql_database.this.name
}

output "owner_role" {
  description = "Role which owns the database and its tables. Nobody logs in with it."
  value       = postgresql_role.owner.name
}

output "migrator_role" {
  description = "Role used by the application to create and migrate its tables."
  value       = postgresql_role.migrator.name
}

output "readwrite_role" {
  description = "Role used by the application to read and write the data."
  value       = "${var.name}_rw"
}

output "roles" {
  description = "All the roles which use the data, with their access."
  value       = { for name, role in local.users : name => role.access }
}
