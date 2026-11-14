#!/usr/bin/env python3
"""Print the matrix of the jobs of the rds-test workflow, as JSON.

    tests/rds_matrix.py <versions> <cli> [<tests>]

    versions  major versions of PostgreSQL separated by spaces or commas, or "all"
    cli       terraform, opentofu or both
    tests     a go test -run pattern; without it the whole test suite is run

The whole suite needs more than two hours on one instance: it is split in parts
("shards") which run at the same time, each one on its own instance. The last
shard runs every test which is not in another one, so a new test is never left out.
"""
import json
import sys

ALL_VERSIONS = ["12", "13", "14", "15", "16", "17", "18"]

# Shard -> pattern of the names of its tests. About the same duration each (15 to 30 minutes).
SHARDS = {
    "a": r"^TestAccPostgresqlGrant",
    "b": r"^TestAccPostgresql(Role|Schema|SecurityLabel|Server|UserMapping|ReplicationSlot|PhysicalReplicationSlot)",
    "c": r"^TestAccPostgresql(Subscription|Publication)",
}
# The shard of all the other tests
REST = "d"
# The end-to-end checks do not depend on the tests: they run once, with the shortest shard
E2E_SHARD = "b"


def matrix(versions, cli, tests=""):
    versions = versions.replace(",", " ").split()
    if versions == ["all"]:
        versions = ALL_VERSIONS
    for version in versions:
        if version not in ALL_VERSIONS:
            sys.exit(f"unknown version {version}: one of {' '.join(ALL_VERSIONS)}, or all")
    if not versions:
        sys.exit("no version")
    if cli not in ("terraform", "opentofu", "both"):
        sys.exit(f"unknown cli {cli}: terraform, opentofu or both")
    clis = ["terraform", "opentofu"] if cli == "both" else [cli]

    if tests:
        shards = [{"shard": "x", "run": tests, "skip": "", "e2e": True}]
    else:
        shards = [{"shard": name, "run": pattern, "skip": "", "e2e": name == E2E_SHARD} for name, pattern in SHARDS.items()]
        shards.append({"shard": REST, "run": ".", "skip": "|".join(SHARDS.values()), "e2e": REST == E2E_SHARD})

    return [{"pgversion": version, "cli": c, **shard} for version in versions for c in clis for shard in shards]


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    print(json.dumps({"include": matrix(*sys.argv[1:])}))
