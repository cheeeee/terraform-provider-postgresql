---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_user_mapping"
sidebar_current: "docs-postgresql-resource-postgresql_user_mapping"
description: |-
  Creates and manages a user mapping on a PostgreSQL server.
---

# postgresql\_user\_mapping

The ``postgresql_user_mapping`` resource creates and manages a user mapping on a PostgreSQL server.


## Usage

```hcl
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
  server_name = "myserver_postgres"
  fdw_name    = "postgres_fdw"
  options = {
    host   = "foo"
    dbname = "foodb"
    port   = "5432"
  }

  depends_on = [postgresql_extension.ext_postgres_fdw]
}

resource "postgresql_role" "remote" {
  name = "remote"
}

resource "postgresql_user_mapping" "remote" {
  server_name = postgresql_server.myserver_postgres.server_name
  user_name   = postgresql_role.remote.name
  options = {
    user = "admin"
    password = "pass"
  }
}
```

## Argument Reference

* `user_name` - (Required) The name of an existing user that is mapped to foreign server. Use `public` to create a so-called public mapping, which is used when no user-specific mapping is applicable.
Changing this value
  will force the creation of a new resource as this value can only be set
  when the user mapping is created.
* `server_name` - (Required) The name of an existing server for which the user mapping is to be created.
Changing this value
  will force the creation of a new resource as this value can only be set
  when the user mapping is created.
* `options` - (Optional) This clause specifies the options of the user mapping. The options typically define the actual user name and password of the mapping. Option names must be unique. The allowed option names and values are specific to the server's foreign-data wrapper.

## Import

A user mapping can be imported with its user name and its server name joined by a dot, for example:

```
$ terraform import postgresql_user_mapping.remote remote.myserver_postgres
```

The options are read from the server only if the connected user can see them: PostgreSQL hides the options of a user mapping from a user who is neither the mapped user nor the owner of the server. In that case, the options of the state are kept.
