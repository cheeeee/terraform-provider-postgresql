#!/usr/bin/env python3
"""Generate the test matrix of the README from the logs of the CI on GitHub.

    scripts/test_matrix.py --run <id of a run of the "test" workflow> [--rds] [--write README.md]

It is run by the "matrix" job of the test workflow, on the logs of the jobs of its own
run: nobody writes the matrix by hand. A cell is only checked if the tests really ran
and passed:

    ✅   all the tests of the area which ran passed, and at least one ran
    ❌   at least one test failed
    n/a  no test ran because the area cannot work in this environment (see NOT_APPLICABLE)
    —    no test ran for another reason: the area was not tested

    --rds            add what was tested on AWS RDS: for each version of PostgreSQL, the last
                     run of the "rds-test" workflow on GitHub where the whole test suite
                     passed with the two CLIs, on the commit of the run of --run or before it
    --summary FILE   also write the matrix, and whether the README shows it, to this file
                     (the summary of the job)
    --logs DIR       read the logs of the jobs of the test workflow from DIR/*.log instead of
                     downloading them (to try the script: such a matrix says where it comes from)
    --repo OWNER/REPO

The logs are downloaded with the token of GH_TOKEN or GITHUB_TOKEN, or the one of the
GitHub CLI (gh).
"""
import argparse
import collections
import glob
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request

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

# The jobs of the rds-test workflow which make a version tested on AWS RDS: the whole
# test suite (its four parts, see tests/rds_matrix.py) with the two CLIs, in the same run
RDS_WORKFLOW = "rds-test.yml"
RDS_CLIS = ("terraform", "opentofu")
RDS_SHARDS = ("a", "b", "c", "d")

API = "https://api.github.com"


def sh(*cmd):
    return subprocess.run(cmd, check=True, capture_output=True, text=True).stdout


def token():
    for name in ("GH_TOKEN", "GITHUB_TOKEN"):
        if os.environ.get(name):
            return os.environ[name]
    return sh("gh", "auth", "token").strip()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def api(path, raw=False):
    """GET of the API of GitHub. The logs are a redirection to a storage which refuses the token."""
    request = urllib.request.Request(
        API + path, headers={"Authorization": "Bearer " + token(), "Accept": "application/vnd.github+json"})
    try:
        with urllib.request.build_opener(NoRedirect).open(request) as response:
            data = response.read()
    except urllib.error.HTTPError as error:
        if error.code not in (301, 302, 307) or not error.headers.get("Location"):
            raise
        with urllib.request.urlopen(error.headers["Location"]) as response:
            data = response.read()
    return data.decode(errors="replace") if raw else json.loads(data)


def run_jobs(repo, run):
    """The jobs of the last attempt of a run."""
    jobs, page = [], 1
    while True:
        found = api(f"/repos/{repo}/actions/runs/{run}/jobs?per_page=100&page={page}")["jobs"]
        jobs += found
        if len(found) < 100:
            return jobs
        page += 1


def job_log(repo, job):
    return api(f"/repos/{repo}/actions/jobs/{job['id']}/logs", raw=True)


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
        m = re.search(r"\| ?(ok|FAIL): ", line) or re.search(r"\t(ok|FAIL): ", line) or re.search(r"Z (ok|FAIL): ", line)
        if m:
            info.setdefault(checks, []).append(m.group(1))
        if "All the end-to-end checks passed" in line:
            info["e2e_passed"] = True
        if "All the upgrade checks passed" in line:
            info["upgrade_passed"] = True
        if "SKIPPED" in line and "Module examples" in line:
            info["e2e_partial"] = True
        m = re.search(r"Creating instance \S+ \(PostgreSQL ((\d+)[0-9.]*)", line)
        if m:
            info["rds_version"] = m.group(2)
            info["rds_engine"] = m.group(1).rstrip(".")
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


def table(header, rows, align=":---:"):
    out = ["| " + " | ".join(header) + " |", "|" + "|".join(["---"] + [align] * (len(header) - 1)) + "|"]
    out += ["| " + " | ".join(row) + " |" for row in rows]
    return "\n".join(out)


def rds_evidence(repo, head):
    """For each version of PostgreSQL, the last run of the rds-test workflow where every job of
    the version passed, on the commit `head` or on one before it: {version: (run, [(suites, info)])}."""
    ancestors = {}

    def is_ancestor(sha):
        if sha not in ancestors:
            try:
                ancestors[sha] = api(f"/repos/{repo}/compare/{sha}...{head}")["status"] in ("ahead", "identical")
            except urllib.error.HTTPError:
                ancestors[sha] = False
        return ancestors[sha]

    evidence = {}
    runs = api(f"/repos/{repo}/actions/workflows/{RDS_WORKFLOW}/runs?status=completed&per_page=40")["workflow_runs"]
    for run in sorted(runs, key=lambda r: r["created_at"], reverse=True):
        passed = collections.defaultdict(dict)
        for job in run_jobs(repo, run["id"]):
            m = re.match(r"rds \((\d+), (\w+), (\w+)\)$", job["name"])
            if m and job["conclusion"] == "success":
                passed[m.group(1)][(m.group(2), m.group(3))] = job
        for version, jobs in passed.items():
            if version in evidence:
                continue
            if not all((cli, shard) in jobs for cli in RDS_CLIS for shard in RDS_SHARDS):
                continue
            if not is_ancestor(run["head_sha"]):
                continue
            evidence[version] = (run, [parse_log(job_log(repo, job)) for job in jobs.values()])
    return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--run", help='id of a run of the "test" workflow')
    parser.add_argument("--logs", help="directory of the logs of the jobs (*.log), named after the jobs")
    parser.add_argument("--rds", action="store_true", help='add the runs of the "rds-test" workflow on GitHub')
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", "cheeeee/terraform-provider-postgresql"))
    parser.add_argument("--write", help="file to update between the test-matrix markers")
    parser.add_argument("--summary", help="file to append the matrix to, with the state of the file of --write")
    args = parser.parse_args()
    if not args.run and not args.logs:
        parser.error("--run or --logs is required")

    # (version, cli) -> (suites, info)
    jobs = {}
    head = None
    incomplete = []
    if args.run:
        run = api(f"/repos/{args.repo}/actions/runs/{args.run}")
        head = run["head_sha"]
        source = ("Updated by the `matrix` job of the [test workflow](.github/workflows/test.yml), from the logs of "
                  "the jobs of its own run, when it changes")
        for job in run_jobs(args.repo, args.run):
            m = re.match(r"test \((\w+), (\w+)\)", job["name"])
            if m:
                if job["conclusion"] not in ("success", "failure"):
                    incomplete.append(job["name"])
                    continue
                jobs[(m.group(1), m.group(2))] = parse_log(job_log(args.repo, job))
    else:
        source = "Generated by `scripts/test_matrix.py` from logs on a disk, not by the CI"
        for path in glob.glob(os.path.join(args.logs, "*.log")):
            m = re.search(r"test-(\w+)-(\w+)\.log$", path)
            if m:
                jobs[(m.group(1), m.group(2))] = parse_log(open(path, errors="replace").read())
    if incomplete:
        # a matrix without all the jobs would show less than what is tested
        message = "The matrix is not generated: these jobs did not finish: " + ", ".join(sorted(incomplete))
        if args.summary:
            with open(args.summary, "a") as f:
                f.write(message + "\n")
        print(message)
        return
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
    rds = rds_evidence(args.repo, head) if args.rds and head else {}
    rds_versions = sorted(rds, key=version_key)
    header = ["Environment", "Superuser", "Not superuser"]
    if rds:
        header.append("AWS RDS")
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
                # the end-to-end checks run in one job of the version for each CLI
                value = merge_versions({
                    v: FAIL if FAIL in [e2e_mark(info) for _, info in rds[v][1]]
                    else (PASS if PASS in [e2e_mark(info) for _, info in rds[v][1]] else NONE)
                    for v in rds_versions
                })
            else:
                value = merge_versions({
                    v: mark([r for suites, _ in rds[v][1] for tests in suites.values() for r in area_results(tests, pattern)])
                    for v in rds_versions
                })
            row.append(cell(area, "aws", value))
        rows.append(row)
    by_environment = table(header, rows)
    footnotes = "".join(f"\n- [{i}] {reason}." for i, reason in enumerate(notes, 1))

    if rds:
        rds_rows = []
        for v in rds_versions:
            run, results = rds[v]
            engines = sorted({info["rds_engine"] for _, info in results if "rds_engine" in info}, key=version_key)
            rds_rows.append([
                ", ".join(engines) or v, run["created_at"][:10], f"`{run['head_sha'][:7]}`",
                f"[{run['run_number']}]({run['html_url']})",
            ])
        by_rds = (
            "\n\nAWS RDS: for each version of PostgreSQL, the last run of the [rds-test workflow]"
            "(.github/workflows/rds-test.yml) where the whole test suite, the end-to-end checks, a password from AWS "
            "Secrets Manager and the IAM authentication of the provider passed on temporary instances, with "
            "Terraform and OpenTofu. A version which has no such run on the commit of this matrix, or before it, is "
            "not listed.\n\n" + table(["AWS RDS PostgreSQL", "Date", "Commit", "Run"], rds_rows)
        )
    elif args.rds:
        by_rds = (
            "\n\nAWS RDS: no run of the [rds-test workflow](.github/workflows/rds-test.yml) passed with the whole "
            "test suite on the commit of this matrix, or before it."
        )
    else:
        by_rds = ""

    text = f"""{MARKERS[0]}
{source}. Every cell covers {" and ".join(cli_names) or "the CLI of the CI"}.

{by_version}{upgrade_note}

The same tests in the different environments:

{by_environment}

- {PASS}: all the tests of the area which ran passed, and at least one ran. With a version: the tests only run from this version of PostgreSQL.
- {FAIL}: at least one test failed.
- {NA}: the area cannot work in this environment, the reason is given below.
- {NONE}: no test ran, the area has not been tested there.
- "Not superuser" is a local PostgreSQL used with a role which is not a superuser, like the master user of a managed service.{footnotes}{by_rds}
{MARKERS[1]}"""

    changed = None
    if args.write:
        content = open(args.write).read()
        if MARKERS[0] not in content or MARKERS[1] not in content:
            sys.exit(f"markers {MARKERS[0]} ... {MARKERS[1]} not found in {args.write}")
        start, end = content.index(MARKERS[0]), content.index(MARKERS[1]) + len(MARKERS[1])
        changed = content[start:end] != text
        if changed:
            with open(args.write, "w") as f:
                f.write(content[:start] + text + content[end:])
        print(f"{args.write} {'updated' if changed else 'is up to date'}")
    else:
        print(text)

    if args.summary:
        with open(args.summary, "a") as f:
            f.write(f"## Test matrix of this run (commit `{(head or 'unknown')[:7]}`)\n\n")
            f.write(text.replace(MARKERS[0] + "\n", "").replace("\n" + MARKERS[1], "") + "\n\n")
            if changed is not None:
                f.write(f"`{args.write}` " + ("does not show this matrix: it differs from the one above.\n" if changed
                                              else "shows this matrix.\n"))
    if os.environ.get("GITHUB_OUTPUT") and changed is not None:
        with open(os.environ["GITHUB_OUTPUT"], "a") as f:
            f.write(f"changed={'true' if changed else 'false'}\n")


main()
