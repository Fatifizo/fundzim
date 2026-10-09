#!/usr/bin/env python3
"""Read `go test -json` from stdin, echo the plain test output, and emit a GitHub Actions ::error annotation
per failed test with the tail of its output (job logs need authentication; annotations are visible to
anyone who can see the run). Exits 1 if any test or package failed."""
import json
import sys

outputs, failed = {}, []
pkg_failed = False
for line in sys.stdin:
    try:
        e = json.loads(line)
    except ValueError:
        print(line, end="")
        continue
    key = (e.get("Package", ""), e.get("Test") or "")
    if e.get("Action") == "output":
        out = e.get("Output", "")
        sys.stdout.write(out)
        outputs.setdefault(key, []).append(out)
    elif e.get("Action") == "fail":
        if e.get("Test"):
            failed.append(key)
        else:
            pkg_failed = True

def esc(s):
    return s.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")

for pkg, test in failed:
    if "/" in test:
        continue  # subtests are reported through their parent
    tail = "".join(outputs.get((pkg, test), []))[-3000:]
    print(f"::error title=FAIL {test}::{esc(tail)}")
sys.exit(1 if failed or pkg_failed else 0)
