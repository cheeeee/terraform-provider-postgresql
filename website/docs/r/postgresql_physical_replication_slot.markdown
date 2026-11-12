---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_physical_replication_slot"
sidebar_current: "docs-postgresql-resource-postgresql_physical_replication_slot"
description: |-
Creates and manages a physical replication slot on a PostgreSQL server.
---

# postgresql\_physical\_replication\_slot

The ``postgresql_physical_replication_slot`` resource creates and manages a physical replication slot on a PostgreSQL
server. This is useful to setup a cross datacenter replication, with Patroni for example, or permit
any stand-by cluster to replicate physically data.


## Usage

```hcl
resource "postgresql_physical_replication_slot" "my_slot" {
  name  = "my_slot"
}
```

## Argument Reference

* `name` - (Required) The name of the replication slot.
* `immediately_reserve` - (Optional) Reserves the WAL immediately when the slot is created, so that a standby can always catch up. Read back as whether the slot has reserved WAL. Changing it to `true` replaces the slot. The WAL reserved by a slot cannot be released, so setting it to `false` on a slot which has reserved WAL does not replace the slot.

## Import

A physical replication slot can be imported with its name, e.g.

```
$ terraform import postgresql_physical_replication_slot.my_slot my_slot
```

Only physical replication slots can be imported: a logical replication slot with the same name is not imported.
