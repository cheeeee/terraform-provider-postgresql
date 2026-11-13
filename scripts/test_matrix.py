#!/usr/bin/env python3
"""Generate the test matrix of the README from the logs of the CI.

    scripts/test_matrix.py --run <id of a run of the "test" workflow> [--write README.md]

The logs of the jobs are downloaded with the GitHub CLI (gh). A cell of the
matrix is only checked if the tests really ran and passed:

    ✅   all the tests of the area which ran passed, and at least one ran
    ❌   at least one test failed
    n/a  no test ran because the area cannot work in this environment (see NOT_APPLICABLE)
    —    no test ran for another reason: the area was not tested

Other sources:
    --logs DIR       read the logs of the jobs from DIR/*.log instead of downloading them
    --rds-logs FILE  logs of a run of the "rds-test" workflow (real AWS RDS), can be repeated
    --repo OWNER/REPO
"""
import argparse
import collections
import glob
import json
import os
import re
import subprocess
import sys

# Area of the provider -> pattern of the names of its tests
AREAS = [
    ("Roles", r"^TestAccPostgresqlRole_(?!Parameters)|^TestAccPostgresqlGrantRole"),
    ("Databases", r"^TestAccPostgresqlDatabase_(?!Parameters)"),
    ("Role and database parameters", r"^TestAccPostgresql(Role|Database)_Parameters|^TestAccConfigParameters"),
    ("Schemas", r"^TestAccPostgresqlSchema"),
    ("Grants", r"^TestAccPostgresql(Grant(?!Role)|ImplicitGrants)"),
    ("Default privileges", r"^TestAccPostgresqlDefaultPrivileges"),
    ("Extensions", r"^TestAccPostgresqlExtension"),
    ("Functions", r"^TestAccPostgresqlFunction"),
    ("Event triggers", r"^TestAccPostgresqlEventTrigger"),
    ("Publications", r"^TestAccPostgresqlPublication"),
    ("Subscriptions", r"^TestAccPostgresqlSubscription"),
    ("Replication slots", r"^TestAccPostgresql(Physical)?ReplicationSlot"),
    ("Foreign servers and user mappings", r"^TestAccPostgresql(Server|UserMapping)"),
    ("Security labels", r"^TestAccPostgresqlSecurityLabel"),
    ("Data sources", r"^TestAccPostgresqlDataSource"),
]
E2E = "End-to-end plan checks"
UPGRADE = "Upgrade from the released versions"

# (area, environment) -> why the area cannot be tested in this environment.
# It is only used when no test of the area ran there: a test which runs is never hidden.
NOT_APPLICABLE = {
    ("Event triggers", "rds"): "creating an event trigger needs a superuser",
    ("Foreign servers and user mappings", "rds"): "creating the extension of a foreign data wrapper needs a superuser",
    ("Security labels", "aws"): "AWS RDS has no security label provider",
    (E2E, "rds"): "the end-to-end test needs privileges of the master user of a managed service (extensions, pgaudit), "
                  "it is run without superuser on AWS RDS",
}
MARKERS = ("<!-- test-matrix:start -->", "<!-- test-matrix:end -->")
PASS, FAIL, NONE, NA = "✅", "❌", "—", "n/a"


def sh(*cmd):
    return subprocess.run(cmd, check=True, capture_output=True, text=True).stdout


def parse_log(text):
    """Returns the results of the tests of a job, for each suite of tests."""
    suites = collections.defaultdict(dict)
    info = {"parameters": {}}
    suite = "tests"
    checks = "e2e"
    upgrade_from = ""
    for line in text.splitlines():
        m = re.search(r"## ->\s+run \((\w+)\)", line)
        if m:
            suite = m.group(1)
            continue
        m = re.search(r"--- (PASS|FAIL|SKIP): (\S+)", line)
        if m:
            # the subtests have generated names
            name = m.group(2).split("/")[0]
            if suites[suite].get(name) != "FAIL":
                suites[suite][name] = m.group(1)
            continue
        m = re.search(r"(\d+) settable parameters found", line)
        if m:
            info["parameters"].setdefault(suite, []).append(int(m.group(1)))
        m = re.search(r"## ((?:OpenTofu|Terraform) v[0-9.]+)", line)
        if m:
            info["cli"] = m.group(1)
        m = re.search(r"## Upgrade from \S+?/(\w+/\w+) ", line)
        if m:
            # the checks which follow are the ones of the upgrade test
            checks = "upgrade"
            upgrade_from = m.group(1)
        m = re.search(r"## Installed (?:\S+/)?(\w+/\w+) (v[0-9.]+)", line)
        if m and checks == "upgrade":
            info.setdefault("upgrade_from", {})[m.group(1)] = m.group(2)
        if "skipped: the released provider cannot read the version" in line:
            info.setdefault("upgrade_skipped", set()).add(upgrade_from)
        m = re.search(r"\| ?(ok|FAIL): ", line) or re.search(r"\t(ok|FAIL): ", line)
        if m:
            info.setdefault(checks, []).append(m.group(1))
        if "All the end-to-end checks passed" in line:
            info["e2e_passed"] = True
        if "All the upgrade checks passed" in line:
            info["upgrade_passed"] = True
        if "SKIPPED" in line and "Module examples" in line:
            info["e2e_partial"] = True
        m = re.search(r"Creating instance \S+ \(PostgreSQL (\d+)", line)
        if m:
            info["rds_version"] = m.group(1)
    return suites, info


def mark(results):
    """Mark of a list of results (PASS / FAIL / SKIP) of tests."""
    if "FAIL" in results:
        return FAIL
    return PASS if "PASS" in results else NONE


def merge(marks):
    marks = set(marks)
    if FAIL in marks:
        return FAIL
    if marks == {PASS}:
        return PASS
    if PASS in marks:
        # passed in some jobs and did not run in other ones
        return PASS + " / " + NONE
    return NONE


def merge_versions(marks):
    """Merge of the marks of an area by version of PostgreSQL: {version: mark}.
    When the tests only run from a version, it is given with the mark."""
    values = set(marks.values())
    if values != {PASS, NONE}:
        return merge(values)
    versions = sorted(marks, key=version_key)
    passed = [v for v in versions if marks[v] == PASS]
    if versions[-len(passed):] == passed:
        return f"{PASS} ({passed[0]}+)"
    return f"{PASS} ({', '.join(passed)})"


# Tests which only check that a configuration is refused, before anything is created: they pass everywhere
# and do not show that the area works in an environment.
VALIDATION_ONLY = {"TestAccPostgresqlEventTrigger_FilterMultiple", "TestAccPostgresqlEventTrigger_LoginUnsupported"}


def area_results(tests, pattern):
    return [result for name, result in tests.items() if re.search(pattern, name) and name not in VALIDATION_ONLY]


def e2e_mark(info, checks="e2e"):
    if "FAIL" in info.get(checks, []):
        return FAIL
    return PASS if info.get(checks + "_passed") else NONE


def version_key(version):
    return [int(x) for x in re.findall(r"\d+", version)][:1], version


def table(header, rows):
    out = ["| " + " | ".join(header) + " |", "|" + "|".join(["---"] + [":---:"] * (len(header) - 1)) + "|"]
    out += ["| " + " | ".join(row) + " |" for row in rows]
    return "\n".join(out)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--run", help='id of a run of the "test" workflow')
    parser.add_argument("--logs", help="directory of the logs of the jobs (*.log), named after the jobs")
    parser.add_argument("--rds-logs", action="append", default=[], help='logs of a run of the "rds-test" workflow')
    parser.add_argument("--repo", default="cheeeee/terraform-provider-postgresql")
    parser.add_argument("--write", help="file to update between the test-matrix markers")
    args = parser.parse_args()
    if not args.run and not args.logs:
        parser.error("--run or --logs is required")

    # (version, cli) -> (suites, info)
    jobs = {}
    source = ""
    if args.run:
        run = json.loads(sh("gh", "run", "view", args.run, "-R", args.repo, "--json", "jobs,headSha,url,conclusion"))
        source = f"[run {args.run}]({run['url']}) of the CI (commit `{run['headSha'][:7]}`)"
        for job in run["jobs"]:
            m = re.match(r"test \((\w+), (\w+)\)", job["name"])
            if m:
                log = sh("gh", "run", "view", "-R", args.repo, "--job", str(job["databaseId"]), "--log")
                jobs[(m.group(1), m.group(2))] = parse_log(log)
    else:
        source = "local runs of the CI workflows (with act)"
        for path in glob.glob(os.path.join(args.logs, "*.log")):
            m = re.search(r"test-(\w+)-(\w+)\.log$", path)
            if m:
                jobs[(m.group(1), m.group(2))] = parse_log(open(path, errors="replace").read())
    if not jobs:
        sys.exit("no job of the test workflow found")

    versions = sorted({v for v, _ in jobs}, key=version_key)
    clis = sorted({c for _, c in jobs}, reverse=True)
    cli_names = sorted({info["cli"] for _, info in jobs.values() if "cli" in info}, reverse=True)

    # --- areas x versions of PostgreSQL (superuser suite, all the CLIs)
    rows = []
    for area, pattern in AREAS:
        rows.append([area] + [
            merge(mark(area_results(jobs[(v, c)][0]["superuser"], pattern)) for c in clis if (v, c) in jobs)
            for v in versions
        ])
    rows.append([E2E] + [merge(e2e_mark(jobs[(v, c)][1]) for c in clis if (v, c) in jobs) for v in versions])
    # the released providers the upgrade is tested from, and the ones which cannot run on a version
    upgrade_from, upgrade_skipped = {}, collections.defaultdict(list)
    upgrade = [UPGRADE]
    for v in versions:
        infos = [jobs[(v, c)][1] for c in clis if (v, c) in jobs]
        value = merge(e2e_mark(info, "upgrade") for info in infos)
        for info in infos:
            upgrade_from.update(info.get("upgrade_from", {}))
        skipped = sorted(set().union(*(info.get("upgrade_skipped", set()) for info in infos)))
        if skipped and value == PASS:
            value += " \\*"
            for name in skipped:
                upgrade_skipped[name].append(v)
        upgrade.append(value)
    rows.append(upgrade)
    upgrade_note = ""
    if upgrade_from:
        released = " and ".join(f"`{name}` {version}" for name, version in sorted(upgrade_from.items()))
        upgrade_note = (
            f"\n\n\"{UPGRADE}\": a configuration with a resource of every type is applied with {released}, "
            "then the plan of this version must be empty."
        )
        for name, skipped_versions in sorted(upgrade_skipped.items()):
            upgrade_note += (
                f" \\* On PostgreSQL {', '.join(skipped_versions)} only the other one is tested: "
                f"the released `{name}` cannot read the version of this server."
            )
    counts = []
    for v in versions:
        found = {tuple(jobs[(v, c)][1]["parameters"].get("superuser", [])) for c in clis if (v, c) in jobs}
        counts.append(" / ".join(str(n) for n in sorted(found.pop(), reverse=True)) if len(found) == 1 else "?")
    rows.append(["Parameters tested (database / role)"] + counts)
    by_version = table(["PostgreSQL"] + versions, rows)

    # --- areas x environments
    rds = [parse_log(open(path, errors="replace").read()) for path in args.rds_logs]
    rds_versions = sorted({info.get("rds_version", "?") for _, info in rds}, key=version_key)
    header = ["Environment", "Superuser", "Not superuser"]
    if rds:
        header.append("AWS RDS (PostgreSQL " + ", ".join(rds_versions) + ")")
    notes = []

    def cell(area, environment, value):
        """A cell without any test is n/a only if the reason is declared."""
        reason = NOT_APPLICABLE.get((area, environment))
        if value == NONE and reason:
            if reason not in notes:
                notes.append(reason)
            return f"{NA} [{notes.index(reason) + 1}]"
        return value

    rows = []
    for area, pattern in AREAS + [(E2E, None)]:
        row = [area]
        for suite in ("superuser", "rds"):
            if pattern is None:
                # the end-to-end test is only run as a superuser by the CI
                value = merge(e2e_mark(info) for _, info in jobs.values()) if suite == "superuser" else NONE
            else:
                value = merge_versions({
                    v: merge(mark(area_results(jobs[(v, c)][0][suite], pattern)) for c in clis if (v, c) in jobs)
                    for v in versions
                })
            row.append(cell(area, suite, value))
        if rds:
            if pattern is None:
                value = merge(e2e_mark(info) for _, info in rds)
            else:
                value = merge(
                    mark([r for tests in suites.values() for r in area_results(tests, pattern)]) for suites, _ in rds
                )
            row.append(cell(area, "aws", value))
        rows.append(row)
    by_environment = table(header, rows)
    footnotes = "".join(f"\n- [{i}] {reason}." for i, reason in enumerate(notes, 1))

    text = f"""{MARKERS[0]}
Generated by `scripts/test_matrix.py` from {source}. Every cell covers {" and ".join(cli_names) or "the CLI of the CI"}.

{by_version}{upgrade_note}

The same tests in the different environments:

{by_environment}

- {PASS}: all the tests of the area which ran passed, and at least one ran. With a version: the tests only run from this version of PostgreSQL.
- {FAIL}: at least one test failed.
- {NA}: the area cannot work in this environment, the reason is given below.
- {NONE}: no test ran, the area has not been tested there.
- "Not superuser" is a local PostgreSQL used with a role which is not a superuser, like the master user of a managed service.{footnotes}
{MARKERS[1]}"""

    if not args.write:
        print(text)
        return
    content = open(args.write).read()
    if MARKERS[0] not in content or MARKERS[1] not in content:
        sys.exit(f"markers {MARKERS[0]} ... {MARKERS[1]} not found in {args.write}")
    start, end = content.index(MARKERS[0]), content.index(MARKERS[1]) + len(MARKERS[1])
    with open(args.write, "w") as f:
        f.write(content[:start] + text + content[end:])
    print(f"{args.write} updated")


main()
