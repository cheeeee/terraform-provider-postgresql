---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_grant_role"
sidebar_current: "docs-postgresql-resource-postgresql_grant_role"
description: |-
  Creates and manages membership in a role to one or more other roles.
---

# postgresql\_grant\_role

The ``postgresql_grant_role`` resource creates and manages membership in a role to one or more other roles in a non-authoritative way.

When using ``postgresql_grant_role`` resource it is likely because the PostgreSQL role you are modifying was created outside of this provider.

~> **Note:** This resource needs PostgreSQL version 9 or above. `with_inherit_option` and `with_set_option` need PostgreSQL 16 or above, they are refused by the older versions.

## Usage

```hcl
resource "postgresql_grant_role" "grant_root" {
  role              = "root"
  grant_role        = "application"
  with_admin_option = true
}
```

~> **Note:** If you use `postgresql_grant_role` for a role that you also manage with a `postgresql_role` resource, you need to ignore the changes of the `roles` attribute in the `postgresql_role` resource or they will fight over what your role grants should be. e.g.:
```hcl
resource "postgresql_role" "bob" {
  name = "bob"

  lifecycle {
    ignore_changes = [
      roles,
    ]
  }
}

resource "postgresql_grant_role" "bob_admin" {
  role       = "bob"
  grant_role = "admin"
}
```

## Argument Reference

* `role` - (Required) The name of the role that is granted a new membership.
* `grant_role` - (Required) The name of the role that is added to `role`.
* `with_admin_option` - (Optional) Giving ability to grant membership to others or not for `role`. (Default: false)
  It is changed in place: `GRANT ... WITH ADMIN OPTION` to enable it, `REVOKE ADMIN OPTION FOR` to disable it.
* `with_inherit_option` - (Optional) Whether `role` uses the privileges of `grant_role` (`WITH INHERIT`, PostgreSQL 16+).
  When it is not set, PostgreSQL chooses: the value of the `inherit` attribute of `role` when the membership is granted.
  It is changed in place (`GRANT ... WITH INHERIT TRUE`, `REVOKE INHERIT OPTION FOR`).
* `with_set_option` - (Optional) Whether `role` can use `SET ROLE` to `grant_role` (`WITH SET`, PostgreSQL 16+).
  When it is not set, PostgreSQL chooses `true`. It is changed in place (`GRANT ... WITH SET TRUE`, `REVOKE SET OPTION FOR`).
* `grantor` - (Optional) The role recorded as the grantor of the membership (`GRANTED BY`).
  Only a superuser can name a role other than the connected user. Since PostgreSQL 16 the grantor
  needs the `ADMIN` option on `grant_role` and a membership can have several grantors, before only one.
  When it is not set, the grantor is the connected user (the bootstrap superuser for a superuser).
  Changing it grants the membership again.

The options which are not set in the configuration are read from `pg_auth_members` and left as they are.

~> **Note:** the `CREATE ROLE ... ROLE` and `ADMIN` clauses (the roles which are members of the new role)
are expressed with a `postgresql_grant_role` per member, with `with_admin_option = true` for `ADMIN`.

## Import

A membership is imported with the ID `<role>_<grant_role>`, which is also its ID in the state
(the option `with_admin_option` is not part of it, the options are changed in place).
The former ID `<role>_<grant_role>_<with_admin_option>` is still accepted.
