#!/usr/bin/env python3
"""Restore-drill user (Stage 18, docs/stage-18/backup-recovery.md §5).

  drill_user.py prepare <state-file> [api-base] [mailpit-base]   register + verify a TEST user on the running stack
  drill_user.py check   <state-file> <api-base>                  log in to a (restored) API and read /me

The state file (mode 600) holds the generated TEST credential; it is written to the drill's scratch directory and never
committed. Output never prints the password.
"""
import http.cookiejar
import json
import os
import re
import secrets
import sys
import time
import urllib.request


def call(opener, method, url, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json", "Accept": "application/json"})
    try:
        with opener.open(req, timeout=30) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def prepare(state, api="http://127.0.0.1:8080", mail="http://127.0.0.1:8025"):
    email = f"restore-drill-{secrets.token_hex(4)}@example.test"
    password = "Drill-" + secrets.token_urlsafe(18)
    op = urllib.request.build_opener()
    st, _ = call(op, "POST", api + "/api/v1/auth/register",
                 {"email": email, "password": password, "display_name": "Restore Drill", "accept_terms": True})
    if st != 202:
        sys.exit(f"register: HTTP {st}")
    token = None
    for _ in range(100):
        with urllib.request.urlopen(mail + "/api/v1/search?query=" + urllib.parse.quote("to:" + email) + "&limit=5", timeout=10) as r:
            msgs = json.loads(r.read()).get("messages") or []
        for m in msgs:
            with urllib.request.urlopen(mail + "/api/v1/message/" + m["ID"], timeout=10) as r2:
                text = json.loads(r2.read()).get("Text", "")
            found = re.search(r"token=([A-Za-z0-9_-]{20,})", text)
            if found:
                token = found.group(1)
        if token:
            break
        time.sleep(0.3)
    if not token:
        sys.exit("verification email not received")
    st, _ = call(op, "POST", api + "/api/v1/auth/verify-email", {"token": token})
    if st != 200:
        sys.exit(f"verify-email: HTTP {st}")
    fd = os.open(state, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump({"email": email, "password": password}, f)
    print(json.dumps({"prepared": email}))


def check(state, api):
    cred = json.load(open(state))
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    st, body = call(op, "POST", api + "/api/v1/auth/login", {"email": cred["email"], "password": cred["password"]})
    status = (body.get("data") or {}).get("status")
    if st != 200 or status != "authenticated":
        print(json.dumps({"login_http": st, "login_status": status}))
        return 1
    st, me = call(op, "GET", api + "/api/v1/me")
    ok = st == 200 and (me.get("data") or {}).get("email") == cred["email"]
    st2, bad = call(urllib.request.build_opener(), "POST", api + "/api/v1/auth/login", {"email": cred["email"], "password": "wrong-" + secrets.token_hex(8)})
    wrong_refused = st2 == 401
    print(json.dumps({"login": "authenticated", "me_matches": ok, "wrong_password_refused": wrong_refused}))
    return 0 if ok and wrong_refused else 1


if __name__ == "__main__":
    if len(sys.argv) >= 3 and sys.argv[1] == "prepare":
        prepare(sys.argv[2], *sys.argv[3:5])
    elif len(sys.argv) == 4 and sys.argv[1] == "check":
        sys.exit(check(sys.argv[2], sys.argv[3]))
    else:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
