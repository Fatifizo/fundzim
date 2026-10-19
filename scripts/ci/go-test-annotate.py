#!/usr/bin/env python3
"""Read `go test -json` from stdin, echo the plain test output, and emit a GitHub Actions ::error annotation
per failed test with the tail of its output (job logs need authentication; annotations are visible to
anyone who can see the run). Exits 1 if any test or package failed."""
import json
import sys

outputs, failed, passed, skipped = {}, [], [], []
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
    elif e.get("Action") == "pass" and e.get("Test") and "/" not in e["Test"]:
        passed.append(key)
    elif e.get("Action") == "skip" and e.get("Test") and "/" not in e["Test"]:
        skipped.append(key)
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
# public summary (job logs need authentication): counts, skipped tests, and how many Stage 8 payment tests passed
top_failed = [k for k in failed if "/" not in k[1]]
pay = sum(1 for _, t in passed if t.startswith("TestPayments"))
pout = sum(1 for _, t in passed if t.startswith("TestPayout"))
fee = sum(1 for _, t in passed if t.startswith("TestFee"))
risk = sum(1 for _, t in passed if t.startswith("TestRisk"))
ops = sum(1 for _, t in passed if t.startswith("TestOperations"))
notif = sum(1 for _, t in passed if t.startswith("TestNotifications"))
names = ", ".join(sorted(t for _, t in skipped)) or "none"
print(f"::notice title=Go test summary::passed {len(passed)}, failed {len(top_failed)}, skipped {len(skipped)} "
      f"(skipped: {esc(names)}); TestPayments* passed: {pay}; TestPayout* passed: {pout}; TestFee* passed: {fee}; TestRisk* passed: {risk}; TestOperations* passed: {ops}; TestNotifications* passed: {notif}")
sys.exit(1 if failed or pkg_failed else 0)
