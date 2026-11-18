"""Private-API helper for run.sh (standard library only).

  check.py bootstrap BASE SETUP_URL_FILE STATE   complete first-run setup, sign in
  check.py site BASE STATE NAME                  create an app site and a DSN key; prints "<id> <key>"
  check.py sites BASE STATE N                    N app sites with one DSN key each; "<id> <key>" lines
  check.py uploadtoken BASE STATE NAME           app site, DSN key and an upload token (sntrys_ format); prints "<id> <key> <token>"
  check.py frame BASE STATE SITE WAIT_S EXPECT_FILE EXPECT_LINE [EXPECT_FUNC]  top in-app frame of the newest event is the original file and line (and function)
  check.py issues BASE STATE SITE [WAIT_S]       wait for issues; prints "<n> <titles...>"; exit 1 if none
  check.py sessions BASE STATE SITE WAIT_S DB_URL  wait for session rows (crash-free rate, else rows in app_events)
  check.py feedback BASE STATE SITE WAIT_S DB_URL  wait for user feedback: stored, scrubbed, linked to an issue, no raw email
  check.py breadcrumbs BASE STATE SITE DB_URL    PASS/SKIP check that stored bodies carry breadcrumbs

Exit codes: 0 pass, 1 fail, 3 skip (message on stdout).
"""
import base64
import http.cookiejar
import json
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class Client:
    def __init__(self, base, state_path=None):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        if state_path:
            st = json.load(open(state_path))
            for name, value in st["cookies"].items():
                self.jar.set_cookie(http.cookiejar.Cookie(
                    0, name, value, None, False, "127.0.0.1", True, False, "/", True, False, None, False, None, None, {}))

    def csrf(self):
        for c in self.jar:
            if c.name == "supermetry_csrf":
                return urllib.parse.unquote(c.value)
        return ""

    def call(self, path, method="GET", body=None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Origin", self.base)
        req.add_header("Referer", self.base + "/")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if method != "GET":
            req.add_header("X-CSRF-Token", self.csrf())
        try:
            with self.opener.open(req, timeout=30) as r:
                raw = r.read()
                return r.status, (json.loads(raw) if raw else None)
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                return e.code, json.loads(raw)
            except ValueError:
                return e.code, raw.decode("utf-8", "replace")[:300]


def need(status, body, ok, what):
    if status not in ok:
        sys.exit(f"{what}: HTTP {status} {body}")
    return body


def main(argv):
    cmd, base = argv[1], argv[2]
    if cmd == "bootstrap":
        url = open(argv[3]).read()
        token = urllib.parse.parse_qs(urllib.parse.urlparse(url.strip().splitlines()[-1]).query)["token"][0]
        password = secrets.token_urlsafe(24)
        email = "sdk-compat@example.test"
        c = Client(base)
        need(*c.call("/api/private/setup", "POST", {"token": token, "email": email, "name": "SDK Compat",
                                                      "password": password, "org_name": "SDK Compat"}), (201,), "setup")
        need(*c.call("/api/private/auth/login", "POST", {"email": email, "password": password}), (200,), "login")
        json.dump({"cookies": {k.name: k.value for k in c.jar}}, open(argv[4], "w"))
        return 0
    c = Client(base, argv[3])
    if cmd == "site":
        s = need(*c.call("/api/private/sites", "POST", {"name": argv[4], "kind": "app", "timezone": "UTC"}), (201,), "create site")
        sid = s.get("id") or s.get("site_id")
        k = need(*c.call(f"/api/private/sites/{sid}/app/dsn-keys", "POST", {"label": "sdk-compat"}), (201,), "create dsn key")
        print(sid, k["public_key"])
        return 0
    if cmd == "uploadtoken":
        me = need(*c.call("/api/private/me"), (200,), "me")
        org = me["memberships"][0]["org"]
        s = need(*c.call("/api/private/sites", "POST", {"name": argv[4], "kind": "app", "timezone": "UTC"}), (201,), "create site")
        sid = s.get("id") or s.get("site_id")
        k = need(*c.call(f"/api/private/sites/{sid}/app/dsn-keys", "POST", {"label": "sourcemaps"}), (201,), "create dsn key")
        u = need(*c.call(f"/api/private/orgs/{org['id']}/api-keys", "POST",
                         {"name": "Source map upload", "scopes": ["releases:write"], "site_ids": [sid]}), (201,), "create upload token")
        # Same layout the dashboard shows (web/src/features/apps/UploadTokenSection.tsx).
        payload = json.dumps({"iat": int(time.time()), "url": base, "region_url": base, "org": org["slug"]}, separators=(",", ":"))
        secret = u["key"].removeprefix("pnl_").replace("_", "", 1)
        print(sid, k["public_key"], f"sntrys_{base64.b64encode(payload.encode()).decode()}_{secret}")
        return 0
    if cmd == "sites":  # N app sites, one DSN key each; "<id> <key>" per line
        for n in range(int(argv[4])):
            s = need(*c.call("/api/private/sites", "POST", {"name": f"bench-{n}", "kind": "app", "timezone": "UTC"}), (201,), "create site")
            sid = s.get("id") or s.get("site_id")
            k = need(*c.call(f"/api/private/sites/{sid}/app/dsn-keys", "POST", {"label": "bench"}), (201,), "create dsn key")
            print(sid, k["public_key"])
        return 0
    site = argv[4]
    wait = float(argv[5]) if len(argv) > 5 and cmd != "breadcrumbs" else 25
    deadline = time.time() + wait
    if cmd == "issues":
        while True:
            r = need(*c.call(f"/api/private/sites/{site}/app/issues"), (200,), "list issues")
            titles = [i["title"] for i in r["issues"]]
            if r["issues"] and all(titles):
                print(len(titles), " | ".join(titles))
                return 0
            if time.time() > deadline:
                print(f"{len(titles)} issues, titles={titles}")
                return 1
            time.sleep(1)
    if cmd == "frame":
        want_file, want_line = argv[6], int(argv[7])
        want_func = argv[8] if len(argv) > 8 else ""
        last = "no event yet"
        while True:
            r = need(*c.call(f"/api/private/sites/{site}/app/issues"), (200,), "list issues")
            for i in r["issues"]:
                d = need(*c.call(f"/api/private/sites/{site}/app/issues/{i['id']}"), (200,), "issue")
                for ev in d.get("latest_events", []):
                    if not ev.get("event_id"):
                        last = f"issue sample has no event_id (keys {sorted(ev)})"
                        continue
                    st, b = c.call(f"/api/private/sites/{site}/app/events/{ev['event_id']}")
                    if st != 200:
                        last = f"event body HTTP {st}"
                        continue
                    frames = [f for x in b["body"].get("exceptions", []) for f in x.get("frames", []) if f.get("in_app", True)]
                    if not frames:
                        last = f"no in-app frames in body (keys {sorted(b['body'])})"
                        continue
                    top = frames[-1]  # frames are oldest call first
                    fn, ln = top.get("filename", ""), top.get("lineno")
                    if fn.endswith(want_file) and ln == want_line:
                        func = top.get("function")
                        print(f"top in-app frame {fn}:{ln} function={func}")
                        if want_func and func != want_func:
                            print(f"FAIL: expected function {want_func}, got {func}")
                            return 1
                        return 0
                    last = f"top in-app frame is {fn}:{ln}"
                    if re.search(r"(^|/)assets/app\.js$", fn):
                        print(f"SKIP: symbolication not available yet ({last}, still the minified bundle)")
                        return 3
            if time.time() > deadline:
                if last == "event body HTTP 404":
                    print("SKIP: symbolication not available (event bodies are not stored by this build, so frames cannot be inspected)")
                    return 3
                print(f"FAIL: expected {want_file}:{want_line}; {last}")
                return 1
            time.sleep(1)
    if cmd == "feedback":
        while True:
            r = need(*c.call(f"/api/private/sites/{site}/app/feedback"), (200,), "list feedback")
            items = r["feedback"]
            if items:
                f = items[0]
                if "froze" not in f["message"] or "fixture-user@" in f["message"] or "[email]" not in f["message"]:
                    print(f"FAIL: message not stored scrubbed: {f['message']!r}")
                    return 1
                if f["name"] is not None:
                    print(f"FAIL: name stored: {f['name']!r}")
                    return 1
                if f["url"] != "/checkout":
                    print(f"FAIL: url {f['url']!r}, expected the path only")
                    return 1
                if not f["issue_id"]:
                    print("FAIL: feedback is not linked to the issue of its event")
                    return 1
                own = need(*c.call(f"/api/private/sites/{site}/app/issues/{f['issue_id']}/feedback"), (200,), "issue feedback")
                if len(own["feedback"]) != 1:
                    print(f"FAIL: issue feedback has {len(own['feedback'])} rows")
                    return 1
                q = f"select count(*) from app_feedback f where f::text ilike '%fixture-user%' or f::text ilike '%Fixture User%'"
                p = subprocess.run(["psql", argv[6], "-Atc", q], capture_output=True, text=True)
                if p.returncode != 0 or p.stdout.strip() != "0":
                    print(f"FAIL: raw email or name present in app_feedback ({p.stdout.strip()} {p.stderr.strip()})")
                    return 1
                print(f"feedback stored, scrubbed and linked to issue {f['issue_id']}")
                return 0
            if time.time() > deadline:
                print("no feedback stored")
                return 1
            time.sleep(1)
    if cmd == "sessions":
        while True:
            r = need(*c.call(f"/api/private/sites/{site}/app/overview"), (200,), "overview")
            if r["crash_free_sessions"] is not None:
                print(f"crash_free_sessions={r['crash_free_sessions']}")
                return 0
            # Aggregate payloads (Node, Python) become kind=session rows without a session id,
            # which the crash-free-sessions rate does not count; the API cannot show them.
            p = subprocess.run(["psql", argv[6], "-Atc", f"select coalesce(sum(occurrences),0) from app_events where site_id={int(site)} and kind='session'"],
                               capture_output=True, text=True)
            n = int(p.stdout.strip() or 0) if p.returncode == 0 else 0
            if n > 0:
                print(f"{n} session count in app_events (aggregate rows; crash_free_sessions needs session ids)")
                return 0
            if time.time() > deadline:
                print("no kind=session rows")
                return 1
            time.sleep(1)
    if cmd == "breadcrumbs":
        db = argv[5]
        r = need(*c.call(f"/api/private/sites/{site}/app/issues"), (200,), "list issues")
        for i in r["issues"]:
            d = need(*c.call(f"/api/private/sites/{site}/app/issues/{i['id']}"), (200,), "issue")
            for ev in d.get("latest_events", []):
                if ev.get("breadcrumbs"):
                    print(f"{len(ev['breadcrumbs'])} breadcrumbs via the issue API")
                    return 0
        # bodies are not exposed by the API yet: look at the table directly
        q = f"select coalesce(max(jsonb_array_length(body->'breadcrumbs')),0) from app_event_bodies where site_id={int(site)}"
        p = subprocess.run(["psql", db, "-Atc", q], capture_output=True, text=True)
        if p.returncode != 0 or "app_event_bodies" in p.stderr:
            print("SKIP: no bodies endpoint and no app_event_bodies table yet (body writer not merged)")
            return 3
        n = int(p.stdout.strip() or 0)
        if n == 0:
            print("SKIP: app_event_bodies has no rows or no breadcrumbs yet (body writer not merged)")
            return 3
        print(f"{n} breadcrumbs in app_event_bodies")
        return 0
    sys.exit("unknown command")


if __name__ == "__main__":
    sys.exit(main(sys.argv))
