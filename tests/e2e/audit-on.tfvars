audit_enabled = true
audit_classes = "ddl, write"

role_parameters = {
  work_mem = "64MB"
}

database_parameters = {
  timezone    = "UTC"
  search_path = "\"$user\", public"
}
