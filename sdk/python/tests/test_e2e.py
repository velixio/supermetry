import json
import os

import pytest

import supermetry_apps as sm

HOST = os.environ.get("SUPERMETRY_E2E_HOST")
APP_ID = os.environ.get("SUPERMETRY_E2E_APP_ID")
KEY = os.environ.get("SUPERMETRY_E2E_KEY")

pytestmark = pytest.mark.skipif(
    not (HOST and APP_ID and KEY),
    reason="set SUPERMETRY_E2E_HOST, SUPERMETRY_E2E_APP_ID and SUPERMETRY_E2E_KEY to run against a real server",
)


def test_session_exception_and_crash_accepted_without_drops():
    from supermetry_apps.client import _urllib_transport

    replies, drops = [], []

    def transport(url, headers, body):
        status, text = _urllib_transport(url, headers, body)
        replies.append((status, text))
        return status, text

    client = sm.Client(
        app_id=APP_ID,
        ingest_key=KEY,
        host=HOST,
        release="0.0.1-e2e",
        environment="e2e",
        install_id=os.environ.get("SUPERMETRY_E2E_INSTALL_ID", "e2e-install-py"),
        flush_interval_ms=0,
        on_drop=lambda events, reason: drops.append(reason),
        transport=transport,
    )
    client.start_session()
    try:
        raise ValueError(os.environ.get("SUPERMETRY_E2E_SECRET_PY", "e2e-secret-message-py"))
    except ValueError as e:
        client.capture_exception(e, handled=True)
    client.capture_crash(error_type="SIGSEGV", exit_reason="signal", signal="SIGSEGV", exit_code=139)
    assert client.flush() is True
    assert len(replies) == 1 and replies[0][0] == 202
    body = json.loads(replies[0][1])
    assert body["accepted"] == 3
    assert body.get("dropped") in (None, [])
    assert drops == []
