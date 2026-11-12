audit_enabled = false

role_parameters = {
  work_mem = "64MB"
}

database_parameters = {
  timezone    = "UTC"
  search_path = "\"$user\", public"
}
