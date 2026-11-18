import json
import re

from fake_server import FakeServer
from supermetry_apps import Client
from supermetry_apps.storage import JsonFileStorage


def setup(**over):
    server = FakeServer()
    sleeps, drops = [], []
    kwargs = dict(
        app_id="app_1",
        ingest_key="k_test",
        host="https://example.test",
        release="1.2.3",
        transport=server.transport,
        flush_interval_ms=0,
        sleep=lambda s: sleeps.append(s),
        random=lambda: 0.5,
        on_drop=lambda events, reason: drops.append((reason, len(events))),
    )
    kwargs.update(over)
    return server, Client(**kwargs), sleeps, drops


def test_request_shape():
    server, client, _, _ = setup()
    client.capture_event("hang", tags={"a": 1})
    client.flush()
    r = server.requests[0]
    assert r["url"] == "https://example.test/api/public/v1/app-events"
    assert r["headers"]["X-Supermetry-Key"] == "k_test"
    assert r["headers"]["Content-Type"] == "application/json"
    assert r["headers"]["User-Agent"].startswith("supermetry-python/")
    assert sorted(r["parsed"]) == ["events", "site"] and r["parsed"]["site"] == "app_1"
    e = r["parsed"]["events"][0]
    assert re.fullmatch(r"[0-9a-f]{32}", e["id"])
    assert e["kind"] == "hang" and e["release"] == "1.2.3" and e["runtime"] == "python"
    assert isinstance(e["ts"], int)
    assert e["platform"] in ("windows", "macos", "linux", "ios", "android", "web", "other")
    assert "install_id" not in e


def test_install_id_only_when_supplied():
    server, client, _, _ = setup(install_id="inst-1")
    client.capture_event("hang")
    client.flush()
    assert server.accepted[0]["install_id"] == "inst-1"


def test_capture_crash():
    server, client, _, _ = setup()
    client.capture_crash(error_type="SIGSEGV", exit_reason="signal", signal="SIGSEGV", exit_code=139)
    client.flush()
    e = server.accepted[0]
    assert (e["kind"], e["level"], e["handled"], e["error_type"], e["exit_code"]) == ("crash", "fatal", False, "SIGSEGV", 139)


def test_session_once_and_tags_later_events():
    server, client, _, _ = setup()
    client.start_session()
    client.start_session()
    client.capture_event("hang")
    client.flush()
    sessions = [e for e in server.accepted if e["kind"] == "session"]
    assert len(sessions) == 1 and re.fullmatch(r"[0-9a-f]{32}", sessions[0]["session_id"])
    assert [e for e in server.accepted if e["kind"] == "hang"][0]["session_id"] == sessions[0]["session_id"]


def test_capture_exception_never_sends_message():
    server, client, _, _ = setup()

    class DiskFullError(Exception):
        pass

    secret = "user bob@example.org wrote /home/bob/secret-notes.txt"
    try:
        raise DiskFullError(secret)
    except DiskFullError as e:
        client.capture_exception(e, handled=True, tags={"op": "save"})
    client.flush()
    body = server.requests[0]["body"]
    assert "secret-notes" not in body and "bob@example.org" not in body and secret not in body
    e = server.accepted[0]
    assert e["kind"] == "error" and e["error_type"] == "DiskFullError" and e["handled"] is True
    assert "test_client.py" in e["frames"] and "/home/" not in e["frames"]


def test_splits_at_20_events():
    server, client, _, _ = setup()
    for _ in range(45):
        client.capture_event("hang")
    client.flush()
    assert [len(r["parsed"]["events"]) for r in server.requests] == [20, 20, 5]


def test_splits_at_16kb():
    server, client, _, _ = setup()
    for i in range(12):
        client.capture_event("hang", frames="\n".join(f"src/f{i}_{j}.py:1 fn" for j in range(100)))
    client.flush()
    assert len(server.requests) > 1
    for r in server.requests:
        assert len(r["body"].encode()) <= 16384 and len(r["parsed"]["events"]) <= 20
    assert len(server.accepted) == 12


def test_bounded_queue_drops_oldest():
    server, client, _, drops = setup(max_queue=3)
    for i in range(5):
        client.capture_event("hang", tags={"i": i})
    client.flush()
    assert [e["tags"]["i"] for e in server.accepted] == [2, 3, 4]
    assert ("queue_full", 1) in drops


def test_retries_with_backoff_and_same_ids():
    server, client, sleeps, _ = setup()
    server.script = [429, 503, "network"]
    client.capture_event("hang")
    client.flush()
    assert len(server.requests) == 4
    assert len({r["parsed"]["events"][0]["id"] for r in server.requests}) == 1
    assert len(server.accepted) == 1
    assert sleeps == [1.0, 2.0, 4.0]


def test_backoff_capped_at_5_minutes():
    server, client, sleeps, _ = setup()
    server.script = [500] * 12
    client.capture_event("hang")
    client.flush(1e9)
    assert max(sleeps) == 300.0


def test_stops_on_401_and_400():
    for status in (401, 400):
        server, client, _, drops = setup()
        server.script = [status]
        client.capture_event("hang")
        client.flush()
        assert len(server.requests) == 1 and drops == [(f"http_{status}", 1)]
        client.flush()
        assert len(server.requests) == 1


def test_set_enabled_false_clears_queue():
    server, client, _, _ = setup()
    client.capture_event("hang")
    client.set_enabled(False)
    client.capture_event("hang")
    client.flush()
    assert server.requests == []
    client.set_enabled(True)
    client.capture_event("hang")
    client.flush()
    assert len(server.accepted) == 1


def test_disabled_at_init():
    server, client, _, _ = setup(enabled=False)
    client.capture_event("hang")
    client.flush()
    assert server.requests == []


def test_discards_too_old():
    now = [1_800_000_000_000]
    server, client, _, drops = setup(now=lambda: now[0], max_age_days=7)
    client.capture_event("hang")
    now[0] += 8 * 86_400_000
    client.capture_event("hang")
    client.flush()
    assert len(server.accepted) == 1 and ("too_old", 1) in drops


def test_flush_returns_when_empty_and_times_out_on_failure():
    server, client, _, _ = setup()
    assert client.flush() is True
    server.script = [503] * 1000
    client.capture_event("hang")
    assert client.flush(0.05) is False


def test_server_side_drops_reported():
    server, client, _, drops = setup()
    server.force_drop = "invalid"
    client.capture_event("hang")
    client.flush()
    assert drops == [("invalid", 1)] and len(server.requests) == 1


def test_json_file_storage_roundtrip(tmp_path):
    path = tmp_path / "queue.json"
    server, client, _, _ = setup(storage=JsonFileStorage(str(path)))
    server.script = [503]
    client.capture_event("hang")
    client.flush(0.01)
    saved = json.loads(path.read_text())
    assert len(saved) == 1
    server2, client2, _, _ = setup(storage=JsonFileStorage(str(path)))
    client2.flush()
    assert server2.accepted[0]["id"] == saved[0]["id"]
    assert json.loads(path.read_text()) == []


def test_corrupt_storage_is_ignored(tmp_path):
    path = tmp_path / "queue.json"
    path.write_text("{not json")
    server, client, _, _ = setup(storage=JsonFileStorage(str(path)))
    client.capture_event("hang")
    client.flush()
    assert len(server.accepted) == 1


def test_shutdown_flushes_then_ignores():
    server, client, _, _ = setup()
    client.capture_event("hang")
    client.shutdown()
    assert len(server.accepted) == 1
    client.capture_event("hang")
    client.flush()
    assert len(server.accepted) == 1


def test_track_sends_event_kind_with_name_and_props():
    server, client, _, _ = setup(install_id="inst-1")
    client.track("purchase", {"plan": "pro", "$release": "9", "n": 5})
    client.flush()
    e = server.accepted[0]
    assert e["kind"] == "event" and e["name"] == "purchase"
    assert e["props"] == {"plan": "pro"}
    assert e["install_id"] == "inst-1"
    assert "screen" not in e and "level" not in e and "handled" not in e and "user_id" not in e


def test_screen_sends_screen_kind():
    server, client, _, _ = setup(install_id="inst-1")
    client.screen("paywall", {"from": "home"})
    client.flush()
    e = server.accepted[0]
    assert e["kind"] == "screen" and e["screen"] == "paywall" and e["props"] == {"from": "home"}
    assert "name" not in e and "level" not in e


def test_track_and_screen_reject_invalid_names_and_missing_install_id():
    server, client, _, _ = setup(install_id="inst-1")
    for bad in ("$app_open", "", "bad/name", "x" * 65):
        client.track(bad)
    client.screen("")
    client.flush()
    assert server.requests == []
    server2, anon, _, _ = setup()
    anon.track("purchase")
    anon.screen("home")
    anon.flush()
    assert server2.requests == []


def test_props_capped_at_20_keys_and_200_bytes():
    server, client, _, _ = setup(install_id="inst-1")
    client.track("many", {f"k{i}": "v" * 300 for i in range(25)})
    client.flush()
    props = server.accepted[0]["props"]
    assert len(props) == 20 and len(props["k0"]) == 200


def test_product_events_batch_by_20():
    server, client, _, _ = setup(install_id="inst-1")
    for i in range(25):
        client.track(f"e{i}")
    client.flush()
    assert [len(r["parsed"]["events"]) for r in server.requests] == [20, 5]


def test_disabled_client_records_no_product_events():
    server, client, _, _ = setup(install_id="inst-1", enabled=False)
    client.track("purchase")
    client.flush()
    assert server.requests == []
