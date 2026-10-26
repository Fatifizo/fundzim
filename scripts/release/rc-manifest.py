#!/usr/bin/env python3
"""Release-candidate manifest for one commit (docs/remediation/phase-2/release-candidate-procedure.md).

Read-only: it reads the commit from git and the commit's CI run from the public GitHub API, and prints a JSON manifest.
It builds nothing, signs nothing, pushes nothing and authorises nothing. It exits non-zero when the commit is not a
valid candidate: a dirty tree, a commit missing from origin, no completed CI run of .github/workflows/ci.yml for that
exact SHA, or a required check that is missing, duplicated or not successful.

Usage: scripts/release/rc-manifest.py [<commit>] [--run <workflow-run-id>]
Unauthenticated API calls are limited to 60 per hour; the script makes about four.
"""
import hashlib
import json
import re
import subprocess
import sys
import urllib.request

REPO = "Fatifizo/fundzim"
WORKFLOW = ".github/workflows/ci.yml"
API = f"https://api.github.com/repos/{REPO}"


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def api(path):
    req = urllib.request.Request(API + path, headers={"Accept": "application/vnd.github+json", "User-Agent": "fundzim-rc-manifest"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def fail(problems, manifest):
    manifest["candidate"] = "REJECTED"
    manifest["problems"] = problems
    print(json.dumps(manifest, indent=2))
    sys.exit(1)


def main():
    args = sys.argv[1:]
    run_id = None
    if "--run" in args:
        i = args.index("--run")
        run_id = args[i + 1]
        del args[i:i + 2]
    sha = git("rev-parse", "--verify", (args[0] if args else "HEAD") + "^{commit}")
    problems = []

    if git("status", "--porcelain"):
        problems.append("working tree is not clean: the manifest must describe committed source only")
    if not git("branch", "-r", "--contains", sha):
        problems.append("commit is not on any origin branch (push it first; CI evidence exists only for pushed commits)")

    # Required checks = the job names of ci.yml at this commit (internal/deploytest TestWorkflowRequiredChecks keeps the
    # list in step with the protection rule).
    workflow_src = git("show", f"{sha}:{WORKFLOW}")
    required = re.findall(r"(?m)^    name: (.+)$", workflow_src)
    migrations = sorted(p for p in git("ls-tree", "--name-only", f"{sha}:migrations").splitlines() if p.endswith(".sql"))

    def blob(path):
        return git("rev-parse", f"{sha}:{path}")

    def pinned(path):
        return sorted(set(re.findall(r"(?m)^\s*(?:FROM|image:)\s+(\S+@sha256:[0-9a-f]{64})", git("show", f"{sha}:{path}"))))

    manifest = {
        "schema": "fundzim-release-candidate/1",
        "repository": REPO,
        "commit": sha,
        "commit_time": git("show", "-s", "--format=%cI", sha),
        "tree": git("rev-parse", f"{sha}^{{tree}}"),
        "inputs": {
            "go.mod": blob("go.mod"),
            "go.sum": blob("go.sum"),
            "apps/web/package-lock.json": blob("apps/web/package-lock.json"),
            "migrations_tree": blob("migrations"),
            "migrations_count": len(migrations),
            "newest_migration": migrations[-1] if migrations else None,
            "base_images": {p: pinned(p) for p in ("deploy/docker/api.Dockerfile", "deploy/docker/web.Dockerfile")},
            "compose_third_party_images": pinned("compose.yaml"),
            "workflow_sha256": hashlib.sha256(workflow_src.encode()).hexdigest(),
        },
        "required_checks": required,
    }

    if run_id:
        run = api(f"/actions/runs/{run_id}")
    else:
        runs = [r for r in api(f"/actions/runs?head_sha={sha}&per_page=50")["workflow_runs"]
                if r["path"] == WORKFLOW and r["status"] == "completed"]
        if not runs:
            problems.append(f"no completed run of {WORKFLOW} for {sha}")
            fail(problems, manifest)
        run = max(runs, key=lambda r: r["created_at"])
    if run["head_sha"] != sha:
        problems.append(f"run {run['id']} is for {run['head_sha']}, not {sha}: evidence from another commit is never proof")
    if run["path"] != WORKFLOW:
        problems.append(f"run {run['id']} comes from {run['path']}, not {WORKFLOW}")
    jobs = api(f"/actions/runs/{run['id']}/jobs?per_page=100")["jobs"]
    by_name = {}
    for j in jobs:
        by_name.setdefault(j["name"], []).append(j)
    checks = {}
    for name in required:
        js = by_name.get(name, [])
        if len(js) != 1:
            problems.append(f"required check {name!r} appears {len(js)} times in run {run['id']}")
            continue
        checks[name] = js[0]["conclusion"]
        if js[0]["conclusion"] != "success":
            problems.append(f"required check {name!r} concluded {js[0]['conclusion']}")
    artifacts = api(f"/actions/runs/{run['id']}/artifacts")["artifacts"]

    manifest["ci"] = {
        "workflow_run_id": run["id"],
        "run_attempt": run.get("run_attempt"),
        "event": run["event"],
        "head_branch": run["head_branch"],
        "conclusion": run["conclusion"],
        "url": run["html_url"],
        "checks": checks,
        "artifacts": [{"name": a["name"], "id": a["id"], "size_in_bytes": a["size_in_bytes"], "digest": a.get("digest"),
                       "expired": a["expired"], "expires_at": a["expires_at"]} for a in artifacts],
    }
    manifest["status"] = {
        # BUILT: the CI run built the images and binaries from this commit (image IDs, binary checksums, SBOMs and Trivy
        # reports are in the supply-chain-evidence artifact, kept 30 days).
        "built": "BUILT_BY_CI" if checks.get("Container images (build, SBOM, vulnerability scan, provenance)") == "success" else "NOT_BUILT",
        # VERIFIED: nothing is signed (no attestation is configured, F-S20-06 / D-12), so no artifact can be verified
        # against this commit by a third party; the provenance record in the artifact is an unsigned statement by CI.
        "provenance": "UNSIGNED_NOT_VERIFIABLE",
        # AUTHORISED: only the owner authorises a release (D-12, D-13, D-14); this script never does.
        "authorisation": "NOT_AUTHORISED",
    }
    if problems:
        fail(problems, manifest)
    manifest["candidate"] = "ELIGIBLE_FOR_REVIEW"
    print(json.dumps(manifest, indent=2))


if __name__ == "__main__":
    main()
