#!/usr/bin/env python3
"""The versions of Terraform and of OpenTofu the provider is tested with (tests/cli_versions.json):
one version, the last one, of every release line of each CLI.

    scripts/cli_versions.py matrix [<postgresql version> ...]
        the jobs of a workflow, as JSON: every version of each CLI, for each version of
        PostgreSQL (17 without any)

    scripts/cli_versions.py check
        compare the file with the releases of the two projects on GitHub: it fails when a
        release line is missing or when a line has a newer version

    scripts/cli_versions.py update
        write the file from the releases on GitHub, from the first line it already has
"""
import json
import os
import re
import subprocess
import sys
import urllib.request

FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "tests", "cli_versions.json")
REPOSITORIES = {"terraform": "hashicorp/terraform", "opentofu": "opentofu/opentofu"}


def key(version):
    return tuple(int(part) for part in version.split("."))


def token():
    for name in ("GH_TOKEN", "GITHUB_TOKEN"):
        if os.environ.get(name):
            return os.environ[name]
    try:
        return subprocess.run(["gh", "auth", "token"], check=True, capture_output=True, text=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return ""


def released(repository):
    """The last version of every release line of a project: {(major, minor): version}."""
    headers = {"Accept": "application/vnd.github+json"}
    if token():
        headers["Authorization"] = "Bearer " + token()
    lines, page = {}, 1
    while True:
        request = urllib.request.Request(
            f"https://api.github.com/repos/{repository}/releases?per_page=100&page={page}", headers=headers)
        with urllib.request.urlopen(request) as response:
            releases = json.load(response)
        for release in releases:
            match = re.fullmatch(r"v(\d+\.\d+\.\d+)", release["tag_name"])
            if match and not release["prerelease"] and not release["draft"]:
                version = match.group(1)
                line = key(version)[:2]
                if line not in lines or key(version) > key(lines[line]):
                    lines[line] = version
        if len(releases) < 100:
            return lines
        page += 1


def expected(current):
    """What the file should be: the last version of every line, from the first line of the file."""
    result = {}
    for cli, repository in REPOSITORIES.items():
        first = min(key(version)[:2] for version in current[cli])
        lines = released(repository)
        result[cli] = [lines[line] for line in sorted(lines) if line >= first]
    return result


def main():
    current = json.load(open(FILE))
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    if command == "matrix":
        versions = sys.argv[2:] or ["17"]
        print(json.dumps({"include": [
            {"pgversion": pgversion, "cli": cli, "version": version}
            for pgversion in versions for cli in REPOSITORIES for version in current[cli]
        ]}))
    elif command in ("check", "update"):
        wanted = expected(current)
        if command == "update":
            with open(FILE, "w") as f:
                json.dump(wanted, f, indent=2)
                f.write("\n")
            print(f"{FILE} written")
            return
        stale = False
        for cli in REPOSITORIES:
            missing = [v for v in wanted[cli] if v not in current[cli]]
            if missing:
                stale = True
                print(f"{cli}: tests/cli_versions.json does not have {', '.join(missing)}")
        if stale:
            sys.exit("Run scripts/cli_versions.py update: the provider is not tested with the versions above.")
        print("tests/cli_versions.json has the last version of every release line")
    else:
        sys.exit(__doc__)


main()
