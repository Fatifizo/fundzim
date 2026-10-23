#!/usr/bin/env python3
"""Backup manifests (Stage 18, docs/stage-18/backup-recovery.md).

  manifest.py write  <backup-dir> <key-file>   < base.json   add file sizes/SHA-256, write manifest.json + manifest.hmac
  manifest.py verify <backup-dir> <key-file>                 check the MAC and every file's SHA-256 (exit 1 on any mismatch)

The MAC key is HMAC-SHA256(key, "fundzim/backup-manifest/v1"): derived from the backup key file under its own label, so
the encryption passphrase is never used directly as a MAC key. Output never contains key material.
"""
import hashlib
import hmac
import json
import os
import sys


def mac_key(key_file):
    raw = open(key_file, "rb").read().strip()
    return hmac.new(raw, b"fundzim/backup-manifest/v1", hashlib.sha256).digest()


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def write(d, key_file):
    m = json.load(sys.stdin)
    m["files"] = {}
    for name in sorted(os.listdir(d)):
        if name.startswith("manifest."):
            continue
        p = os.path.join(d, name)
        m["files"][name] = {"bytes": os.path.getsize(p), "sha256": sha256(p)}
    body = json.dumps(m, indent=2, sort_keys=True).encode()
    open(os.path.join(d, "manifest.json"), "wb").write(body)
    open(os.path.join(d, "manifest.hmac"), "w").write(hmac.new(mac_key(key_file), body, hashlib.sha256).hexdigest() + "\n")
    os.chmod(os.path.join(d, "manifest.json"), 0o600)
    os.chmod(os.path.join(d, "manifest.hmac"), 0o600)


def verify(d, key_file):
    body = open(os.path.join(d, "manifest.json"), "rb").read()
    want = open(os.path.join(d, "manifest.hmac")).read().strip()
    got = hmac.new(mac_key(key_file), body, hashlib.sha256).hexdigest()
    problems = []
    if not hmac.compare_digest(want, got):
        problems.append("manifest MAC does not verify (manifest altered or wrong key)")
    m = json.loads(body)
    for name, f in m["files"].items():
        p = os.path.join(d, name)
        if not os.path.exists(p):
            problems.append(f"{name}: missing")
        elif os.path.getsize(p) != f["bytes"] or sha256(p) != f["sha256"]:
            problems.append(f"{name}: size or SHA-256 differs from the manifest")
    extra = set(os.listdir(d)) - set(m["files"]) - {"manifest.json", "manifest.hmac"}
    if extra:
        problems.append("unexpected files: " + ", ".join(sorted(extra)))
    print(json.dumps({"backup_id": m["backup_id"], "files": len(m["files"]), "problems": problems}))
    return 0 if not problems else 1


if __name__ == "__main__":
    if len(sys.argv) != 4 or sys.argv[1] not in ("write", "verify"):
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    if sys.argv[1] == "write":
        write(sys.argv[2], sys.argv[3])
    else:
        sys.exit(verify(sys.argv[2], sys.argv[3]))
