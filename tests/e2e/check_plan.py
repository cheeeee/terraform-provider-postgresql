#!/usr/bin/env python3
"""Check the changes of a Terraform plan.

usage: terraform show -json <plan> | check_plan.py <name of the check> <expected changes>

The expected changes are a JSON object: for each resource which is expected to
change, its actions and, optionally, the "parameters" attribute before and
after the change. Any other change in the plan is an error, unless the "*" key
is given: it describes the actions of all the changes which are not listed and,
with "count", the total number of changes of the plan.
"""
import json
import sys

name, expected = sys.argv[1], json.loads(sys.argv[2])
plan = json.load(sys.stdin)

changes = {
    rc["address"]: rc["change"]
    for rc in plan.get("resource_changes", [])
    if rc["change"]["actions"] != ["no-op"]
}

errors = []
others = expected.pop("*", None)
for address in sorted(set(changes) - set(expected)):
    if others is None:
        errors.append(f"{address}: unexpected change {changes[address]['actions']}")
    elif changes[address]["actions"] != others["actions"]:
        errors.append(f"{address}: actions are {changes[address]['actions']}, expected {others['actions']}")
if others is not None and "count" in others and len(changes) != others["count"]:
    errors.append(f"{len(changes)} changes are planned, expected {others['count']}: {', '.join(sorted(changes))}")
for address, exp in sorted(expected.items()):
    change = changes.get(address)
    if change is None:
        errors.append(f"{address}: no change planned, expected {exp['actions']}")
        continue
    if change["actions"] != exp["actions"]:
        errors.append(f"{address}: actions are {change['actions']}, expected {exp['actions']}")
    for side in ("before", "after"):
        if side not in exp:
            continue
        actual = (change.get(side) or {}).get("parameters")
        if actual != exp[side]:
            errors.append(f"{address}: parameters {side} the change are {json.dumps(actual, sort_keys=True)}, "
                          f"expected {json.dumps(exp[side], sort_keys=True)}")

if errors:
    print(f"FAIL: {name}")
    for error in errors:
        print(f"  {error}")
    sys.exit(1)
print(f"ok: {name}")
