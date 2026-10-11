#!/usr/bin/env python3
"""Bring the isolated accuracy A/B product stacks up and down."""

from __future__ import annotations

import argparse
import base64
import http.cookiejar
import json
import os
import re
import secrets
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import Any, TypeVar


ROOT = Path(__file__).resolve().parents[4]
RIG = ROOT / "tools" / "bench" / "rig"
DEFAULT_WORKDIR = ROOT / "tools" / "bench" / "results" / "accuracy-ab"
DEFAULT_STATE = DEFAULT_WORKDIR / "stack.json"
KEY_PLACEHOLDER = "__ACCURACY_AB_KEY__"
DATABASE = "accuracy_ab"
DATABASE_URL = "postgres://supermetry:supermetry@127.0.0.1:5433/accuracy_ab?sslmode=disable"
OWNER_EMAIL = "owner@accuracy.example.test"
T = TypeVar("T")


def render_mkkey(key: str) -> str:
    """Render the checked-in RPC script after validating its only input."""
    if re.fullmatch(r"[0-9a-f]{48}", key) is None:
        raise ValueError("Plausible stats key must be 48 lowercase hex characters")
    template = Path(__file__).with_name("mkkey.exs").read_text(encoding="utf-8")
    if template.count(KEY_PLACEHOLDER) != 1:
        raise ValueError("mkkey.exs must contain exactly one key placeholder")
    return template.replace(KEY_PLACEHOLDER, key)


def supermetry_env(workdir: Path, secret: str) -> dict[str, str]:
    """Return the complete environment for the isolated Supermetry process."""
    env = {key: value for key, value in os.environ.items() if not key.startswith("SUPERMETRY_")}
    env.update(
        {
            "SUPERMETRY_DATABASE_URL": DATABASE_URL,
            "SUPERMETRY_SECRET_KEY": secret,
            "SUPERMETRY_BASE_URL": "http://app.test:18090",
            "SUPERMETRY_LISTEN": "127.0.0.1:18090",
            "SUPERMETRY_TRACKING_MODE": "identified",
            "SUPERMETRY_TRUSTED_PROXIES": "127.0.0.1/32",
            "SUPERMETRY_DATA_DIR": str(workdir / "data"),
        }
    )
    return env


def parse_reset_token(output: str) -> str:
    """Extract and URL-decode the reset token printed by ``user create``."""
    for token in re.findall(r"https?://[^\s]+", output):
        value = urllib.parse.parse_qs(urllib.parse.urlsplit(token).query).get("token")
        if value and value[0]:
            return value[0]
    raise ValueError("user create output did not contain a password reset token")


def wait_until(
    fn: Callable[[], T],
    timeout: float,
    interval: float,
    *,
    clock: Callable[[], float] = time.monotonic,
    sleep: Callable[[float], None] = time.sleep,
) -> T:
    """Poll until ``fn`` returns a truthy value or the deadline is reached."""
    deadline = clock() + timeout
    while True:
        value = fn()
        if value:
            return value
        if clock() >= deadline:
            break
        sleep(interval)
    raise TimeoutError(f"timed out after {timeout:g} seconds")


def write_state(path: Path, state: Mapping[str, Any]) -> None:
    """Atomically write private stack state without exposing credentials."""
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(state, handle, indent=2, sort_keys=True)
            handle.write("\n")
        os.replace(temporary, path)
        path.chmod(0o600)
    except BaseException:
        try:
            os.close(descriptor)
        except OSError:
            pass
        Path(temporary).unlink(missing_ok=True)
        raise


def _write_private_text(path: Path, content: str) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(content)
    except BaseException:
        path.unlink(missing_ok=True)
        raise


def _run(
    args: list[str],
    *,
    env: Mapping[str, str] | None = None,
    capture: bool = False,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        cwd=ROOT,
        env=dict(env) if env is not None else None,
        check=True,
        text=True,
        input=input_text,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.STDOUT if capture else None,
    )


def _docker() -> list[str]:
    probe = subprocess.run(
        ["docker", "info"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False
    )
    return ["docker"] if probe.returncode == 0 else ["sudo", "docker"]


def _request_json(
    opener: urllib.request.OpenerDirector,
    method: str,
    url: str,
    body: Mapping[str, Any] | None = None,
    headers: Mapping[str, str] | None = None,
    expected: tuple[int, ...] = (200, 201, 204),
) -> Any:
    data = None if body is None else json.dumps(body).encode()
    request_headers = {"Accept": "application/json", **(dict(headers or {}))}
    if data is not None:
        request_headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=request_headers, method=method)
    try:
        with opener.open(request, timeout=10) as response:
            payload = response.read()
            status = response.status
    except urllib.error.HTTPError as error:
        detail = error.read(500).decode(errors="replace")
        raise RuntimeError(f"{method} {urllib.parse.urlsplit(url).path}: HTTP {error.code}: {detail}") from error
    if status not in expected:
        raise RuntimeError(f"{method} {urllib.parse.urlsplit(url).path}: HTTP {status}")
    return json.loads(payload) if payload else None


def _plausible_query(key: str) -> bool:
    today = time.strftime("%Y-%m-%d", time.gmtime())
    opener = urllib.request.build_opener()
    _request_json(
        opener,
        "POST",
        "http://127.0.0.1:8000/api/v2/query",
        {"site_id": "site1.test", "date_range": [today, today], "metrics": ["visitors"]},
        {"Authorization": f"Bearer {key}"},
    )
    return True


def _warm_plausible() -> bool:
    body = json.dumps(
        {
            "name": "pageview",
            "url": "http://app.test/warmup",
            "domain": "site2.test",
            "referrer": "",
        }
    ).encode()
    request = urllib.request.Request(
        "http://127.0.0.1:8000/api/event",
        data=body,
        headers={
            "Content-Type": "application/json",
            "User-Agent": "Mozilla/5.0 Chrome/129.0.0.0 Safari/537.36",
            "X-Forwarded-For": "203.0.113.254",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return 200 <= response.status < 300 and not response.headers.get("x-plausible-dropped")
    except (OSError, urllib.error.URLError):
        return False


def _create_supermetry_resources(
    env: Mapping[str, str], password: str
) -> tuple[int, str, str, str]:
    binary = ROOT / "bin" / "supermetry"
    created = _run(
        [str(binary), "user", "create", "--email", OWNER_EMAIL, "--org", "Accuracy A/B"],
        env=env,
        capture=True,
    )
    token = parse_reset_token(created.stdout)
    cookies = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
    base = "http://127.0.0.1:18090"
    # Private POSTs must carry an Origin that matches SUPERMETRY_BASE_URL (CSRF protection).
    host = {"Host": "app.test:18090", "Origin": "http://app.test:18090"}
    _request_json(
        opener,
        "POST",
        base + "/api/private/auth/password-reset/confirm",
        {"token": token, "password": password},
        host,
    )
    _request_json(
        opener,
        "POST",
        base + "/api/private/auth/login",
        {"email": OWNER_EMAIL, "password": password, "device_label": "Accuracy A/B"},
        host,
    )
    csrf = next((cookie.value for cookie in cookies if cookie.name == "supermetry_csrf"), "")
    if not csrf:
        raise RuntimeError("Supermetry login did not set the CSRF cookie")
    auth_headers = {**host, "X-CSRF-Token": csrf}
    organizations = _request_json(opener, "GET", base + "/api/private/orgs", headers=auth_headers)
    memberships = organizations.get("data", [])
    if not memberships:
        raise RuntimeError("Supermetry owner has no organization")
    org_id = memberships[0].get("org_id", memberships[0].get("OrgID"))
    if not isinstance(org_id, int):
        raise RuntimeError("Supermetry organization response has no numeric org_id")
    org_headers = {**auth_headers, "X-Supermetry-Org": str(org_id)}
    site = _request_json(
        opener,
        "POST",
        base + "/api/private/sites",
        {
            "name": "Accuracy A/B",
            "hosts": ["app.test"],
            "timezone": "UTC",
            "retention_days": 7,
            "revenue_currency": "USD",
        },
        org_headers,
    )
    site_id = site.get("id", site.get("ID"))
    public_id = site.get("public_id", site.get("PublicID"))
    if not isinstance(site_id, int) or not isinstance(public_id, str):
        raise RuntimeError("Supermetry site response is missing id or public_id")
    # Supermetry keeps referrers, UTM parameters and event properties only for projects that
    # opt in with profiles_enabled (on an identified instance); without it those breakdowns
    # are empty by design. Plausible records them always, so enable it for a fair comparison.
    _request_json(opener, "PATCH", base + f"/api/private/sites/{site_id}", {"profiles_enabled": True}, org_headers)
    snippet = _request_json(
        opener,
        "GET",
        f"{base}/api/private/sites/{site_id}/snippet",
        headers=org_headers,
    )
    snippet_html = snippet.get("snippet", "")
    source = re.search(r'''\bsrc=["']([^"']+)["']''', snippet_html)
    if source is None or f"/js/{public_id}.js" not in source.group(1):
        raise RuntimeError("Supermetry snippet response has no matching tracker script URL")
    created_key = _request_json(
        opener,
        "POST",
        f"{base}/api/private/orgs/{org_id}/api-keys",
        {"name": "accuracy-ab", "scopes": ["stats:read"], "site_ids": [site_id]},
        org_headers,
    )
    api_key = created_key.get("key")
    if not isinstance(api_key, str) or not api_key:
        raise RuntimeError("Supermetry API-key response is missing key")
    return site_id, public_id, source.group(1), api_key


def _container_running(name: str) -> bool:
    result = subprocess.run(
        _docker() + ["inspect", "-f", "{{.State.Running}}", name],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    return result.returncode == 0 and result.stdout.strip() == "true"


def _drop_database(name: str) -> None:
    if name != DATABASE:
        raise ValueError(f"refusing to drop unexpected database {name!r}")
    if not _container_running("supermetry-dev-postgres"):
        return
    _run(
        _docker()
        + [
            "exec",
            "supermetry-dev-postgres",
            "psql",
            "-X",
            "-v",
            "ON_ERROR_STOP=1",
            "-U",
            "supermetry",
            "-d",
            "postgres",
            "-c",
            f'DROP DATABASE IF EXISTS "{DATABASE}" WITH (FORCE)',
        ]
    )


def _create_database() -> None:
    _drop_database(DATABASE)
    _run(
        _docker()
        + [
            "exec",
            "supermetry-dev-postgres",
            "psql",
            "-X",
            "-v",
            "ON_ERROR_STOP=1",
            "-U",
            "supermetry",
            "-d",
            "postgres",
            "-c",
            f'CREATE DATABASE "{DATABASE}" OWNER supermetry',
        ]
    )


def _kill_process(pid: int) -> None:
    if pid <= 1:
        return
    try:
        os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.1)
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def down(
    *,
    state_path: Path = DEFAULT_STATE,
    kill_process: Callable[[int], None] = _kill_process,
    drop_database: Callable[[str], None] = _drop_database,
    down_plausible: Callable[[], None] | None = None,
) -> None:
    """Tear down every component, continuing after individual failures."""
    if down_plausible is None:
        down_plausible = lambda: _run([str(RIG / "down.sh"), "plausible", "--purge"])
    state: dict[str, Any] = {}
    if state_path.exists():
        try:
            state = json.loads(state_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            state = {}
    supermetry = state.get("supermetry", {})
    pid = supermetry.get("pid")
    database = supermetry.get("database", DATABASE)
    errors: list[BaseException] = []
    for cleanup in (
        (lambda: kill_process(pid)) if isinstance(pid, int) else (lambda: None),
        lambda: drop_database(database),
        down_plausible,
        lambda: state_path.unlink(missing_ok=True),
    ):
        try:
            cleanup()
        except BaseException as error:
            errors.append(error)
    if errors:
        raise RuntimeError("; ".join(str(error) for error in errors)) from errors[0]


def up(*, workdir: Path = DEFAULT_WORKDIR) -> dict[str, Any]:
    """Start both isolated stacks and return the persisted connection state."""
    state_path = workdir / "stack.json"
    if state_path.exists():
        raise RuntimeError(f"stack state already exists: {state_path}; run down first")
    workdir.mkdir(parents=True, exist_ok=True)
    process: subprocess.Popen[bytes] | None = None
    log_handle: Any = None
    plausible_touched = False
    database_touched = False
    try:
        if not (RIG / ".env").exists():
            _run([str(RIG / "init-env.sh")])
        plausible_touched = True
        _run([str(RIG / "up.sh"), "plausible"])
        _run([str(RIG / "seed.sh"), "plausible", "2"])
        plausible_key = secrets.token_hex(24)
        docker = _docker()
        # Pipe the script in on stdin: `docker cp` would keep the private 0600 mode
        # of a host file, which the container's own user cannot read (eacces).
        _run(
            docker + ["exec", "-i", "plausible-plausible-1", "sh", "-c", "cat > /tmp/mkkey.exs"],
            input_text=render_mkkey(plausible_key),
        )
        _run(
            docker
            + [
                "exec",
                "plausible-plausible-1",
                "/app/bin/plausible",
                "rpc",
                'Code.eval_file("/tmp/mkkey.exs")',
            ]
        )

        def plausible_query_ready() -> bool:
            try:
                return _plausible_query(plausible_key)
            except (OSError, RuntimeError, urllib.error.URLError):
                return False

        wait_until(plausible_query_ready, 30, 1)

        database_touched = True
        _run(
            _docker()
            + [
                "compose",
                "-f",
                str(ROOT / "deploy" / "docker-compose.dev-db.yml"),
                "up",
                "-d",
                "--wait",
            ]
        )
        _create_database()
        binary = ROOT / "bin" / "supermetry"
        if not binary.exists():
            binary.parent.mkdir(parents=True, exist_ok=True)
            _run(["go", "build", "-o", str(binary), "./cmd/supermetry"])
        secret = base64.b64encode(secrets.token_bytes(32)).decode()
        password = secrets.token_urlsafe(24)
        env = supermetry_env(workdir, secret)
        (workdir / "data").mkdir(parents=True, exist_ok=True)
        _run([str(binary), "migrate"], env=env)
        log_handle = (workdir / "supermetry.log").open("ab", buffering=0)
        process = subprocess.Popen(
            [str(binary), "serve"],
            cwd=ROOT,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=log_handle,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )

        def ready() -> bool:
            try:
                with urllib.request.urlopen("http://127.0.0.1:18090/readyz", timeout=2) as response:
                    return response.status == 200
            except (OSError, urllib.error.URLError):
                assert process is not None
                if process.poll() is not None:
                    raise RuntimeError(f"Supermetry exited with status {process.returncode}")
                return False

        wait_until(ready, 60, 0.2)
        site_id, public_id, script_url, api_key = _create_supermetry_resources(env, password)
        wait_until(_warm_plausible, 90, 1)
        assert process is not None
        state = {
            "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "workdir": str(workdir),
            "plausible": {
                "url": "http://127.0.0.1:8000",
                "domain": "site1.test",
                "key": plausible_key,
                "warmup_domain": "site2.test",
            },
            "supermetry": {
                "url": "http://app.test:18090",
                "api_url": "http://127.0.0.1:18090",
                "listen": "127.0.0.1:18090",
                "site_id": site_id,
                "public_id": public_id,
                "script_url": script_url,
                "key": api_key,
                "database": DATABASE,
                "pid": process.pid,
            },
            "site": {"url": "http://app.test:18091", "port": 18091},
        }
        write_state(state_path, state)
        return state
    except BaseException as error:
        cleanup_errors: list[str] = []
        cleanups: list[Callable[[], None]] = []
        if process is not None:
            cleanups.append(lambda: _kill_process(process.pid))
        if database_touched:
            cleanups.append(lambda: _drop_database(DATABASE))
        if plausible_touched:
            cleanups.append(lambda: _run([str(RIG / "down.sh"), "plausible", "--purge"]))
        cleanups.extend((lambda: state_path.unlink(missing_ok=True),))
        for cleanup in cleanups:
            try:
                cleanup()
            except BaseException as cleanup_error:
                cleanup_errors.append(str(cleanup_error))
        if cleanup_errors:
            error.add_note("cleanup errors: " + "; ".join(cleanup_errors))
        raise
    finally:
        if log_handle is not None:
            log_handle.close()


def status(*, state_path: Path = DEFAULT_STATE) -> dict[str, Any]:
    state_exists = state_path.exists()
    state: dict[str, Any] = {}
    if state_exists:
        state = json.loads(state_path.read_text(encoding="utf-8"))
    pid = state.get("supermetry", {}).get("pid")
    running = False
    if isinstance(pid, int):
        try:
            os.kill(pid, 0)
            running = True
        except ProcessLookupError:
            pass
    result = {"state": state_exists, "supermetry": running, "plausible": False}
    try:
        with urllib.request.urlopen(
            "http://127.0.0.1:8000/api/system/health/ready", timeout=1
        ) as response:
            result["plausible"] = response.status == 200
    except (OSError, urllib.error.URLError):
        pass
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("up", "down", "status"))
    parser.add_argument("--workdir", type=Path, default=DEFAULT_WORKDIR)
    args = parser.parse_args(argv)
    state_path = args.workdir / "stack.json"
    try:
        if args.command == "up":
            up(workdir=args.workdir)
            print(f"stack ready; private state written to {state_path}")
        elif args.command == "down":
            down(state_path=state_path)
            print("stack down")
        else:
            print(json.dumps(status(state_path=state_path), sort_keys=True))
        return 0
    except (OSError, RuntimeError, TimeoutError, ValueError, subprocess.SubprocessError) as error:
        print(f"stack {args.command} failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
