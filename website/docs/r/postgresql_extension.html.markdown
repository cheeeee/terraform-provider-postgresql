---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_extension"
sidebar_current: "docs-postgresql-resource-postgresql_extension"
description: |-
  Creates and manages an extension on a PostgreSQL server.
---

# postgresql\_extension

The ``postgresql_extension`` resource creates and manages an extension on a PostgreSQL
server.


## Usage

```hcl
resource "postgresql_extension" "my_extension" {
  name = "pg_trgm"
}
```

## Argument Reference

* `name` - (Required) The name of the extension.
* `schema` - (Optional) Sets the schema of an extension. When it changes, the extension and its objects are moved with `ALTER EXTENSION ... SET SCHEMA`. Omitting it keeps the current schema. Note that PostgreSQL requires the ownership of the objects of an extension to move them: for a trusted extension created by a non-superuser, the move may be refused.
* `version` - (Optional) Sets the version number of the extension. When it changes, the extension is updated with `ALTER EXTENSION ... UPDATE TO`; only forward updates are possible. Omitting it keeps the installed version (which may not be the default version of the extension).
* `database` - (Optional) Which database to create the extension on. Defaults to provider database.
* `drop_cascade` - (Optional) When true, will also drop all the objects that depend on the extension, and in turn all objects that depend on those objects. (Default: false)
* `create_cascade` - (Optional) When true, will also create any extensions that this extension depends on that are not already installed. (Default: false)

## Import

PostgreSQL Extensions can be imported using the database name and the extension's resource name, e.g.

`terraform import postgresql_extension.uuid_ossp example-database.uuid-ossp`
