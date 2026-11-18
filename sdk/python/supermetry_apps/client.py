import json
import platform as _platform
import random as _random
import re
import secrets
import sys
import threading
import time
import traceback
import urllib.error
import urllib.request
from typing import Any, Callable, Dict, List, Optional, Tuple

from .frames import build_frames, frames_from_traceback, scrub_frames

__version__ = "0.1.0"

MAX_BATCH_EVENTS = 20
MAX_BODY_BYTES = 16 * 1024
BACKOFF_BASE_S = 1.0
BACKOFF_CAP_S = 300.0
DAY_MS = 86_400_000

Transport = Callable[[str, Dict[str, str], str], Tuple[int, str]]


def _urllib_transport(url: str, headers: Dict[str, str], body: str) -> Tuple[int, str]:
    req = urllib.request.Request(url, data=body.encode("utf-8"), headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=10) as res:
            return res.status, res.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, ""


def _clip(s: str, n: int) -> str:
    return s[:n]


_EVENT_NAME = re.compile(r"[A-Za-z0-9_.:$ -]+")


def _clip_bytes(s: str, n: int) -> str:
    while len(s.encode("utf-8")) > n:
        s = s[:-1]
    return s


def _clean_props(props: Optional[Dict[str, Any]]) -> Optional[Dict[str, str]]:
    """Custom event props: string values only, no "$" keys (reserved), at most 20 keys."""
    if not props:
        return None
    out: Dict[str, str] = {}
    for k, v in props.items():
        if len(out) >= 20:
            break
        if not isinstance(k, str) or not isinstance(v, str) or not k or k.startswith("$") or len(k.encode("utf-8")) > 64:
            continue
        out[k] = _clip_bytes(v, 200)
    return out or None


def _clean_tags(tags: Optional[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
    if not tags:
        return None
    out: Dict[str, Any] = {}
    for k, v in tags.items():
        if len(out) >= 20:
            break
        if isinstance(v, str):
            while len(v.encode("utf-8")) > 200:
                v = v[:-1]
            out[_clip(str(k), 64)] = v
        elif isinstance(v, (bool, int, float)):
            out[_clip(str(k), 64)] = v
    return out or None


def _detect_platform() -> Dict[str, Optional[str]]:
    system = sys.platform
    plat = "windows" if system.startswith("win") else "macos" if system == "darwin" else "linux" if system.startswith("linux") else "other"
    machine = _platform.machine().lower()
    arch = {"x86_64": "x64", "amd64": "x64", "arm64": "arm64", "aarch64": "arm64", "i386": "x86", "i686": "x86", "x86": "x86"}.get(machine)
    if arch is None:
        arch = "arm" if machine.startswith("arm") else "other"
    raw = ""
    try:
        if plat == "macos":
            raw = _platform.mac_ver()[0]
        elif plat == "windows":
            raw = _platform.version()
        else:
            raw = _platform.release()
    except Exception:
        raw = ""
    parts = raw.split(".")
    osv = ".".join(p for p in parts[:2] if p.isdigit()) or None
    return {"platform": plat, "arch": arch, "os_version": osv}


class Client:
    def __init__(
        self,
        app_id: str,
        ingest_key: str,
        host: str,
        release: str,
        build: Optional[str] = None,
        environment: Optional[str] = None,
        install_id: Optional[str] = None,
        enabled: bool = True,
        max_queue: int = 100,
        flush_interval_ms: int = 5000,
        max_age_days: int = 7,
        transport: Optional[Transport] = None,
        storage: Any = None,
        on_drop: Optional[Callable[[List[dict], str], None]] = None,
        in_app: Optional[Callable[[str], bool]] = None,
        os_version: Optional[str] = None,
        device_class: Optional[str] = None,
        sleep: Optional[Callable[[float], None]] = None,
        random: Optional[Callable[[], float]] = None,
        now: Optional[Callable[[], int]] = None,
    ) -> None:
        self.app_id, self.ingest_key, self.host = app_id, ingest_key, host.rstrip("/")
        self.release, self.build, self.environment, self.install_id = release, build, environment, install_id
        self.max_queue, self.max_age_days = max_queue, max_age_days
        self.storage, self.on_drop, self.in_app = storage, on_drop, in_app
        self.device_class = device_class
        self._enabled = enabled
        self._closed = False
        self._transport = transport or _urllib_transport
        self._sleep = sleep or time.sleep
        self._random = random or _random.random
        self._now = now or (lambda: int(time.time() * 1000))
        self._plat = _detect_platform()
        if os_version:
            self._plat["os_version"] = os_version
        self._queue: List[dict] = []
        self._lock = threading.RLock()
        self._drain_lock = threading.Lock()
        self._session_id: Optional[str] = None
        self._failures = 0
        self._next_attempt_at = 0.0
        self._stop = threading.Event()
        if storage is not None and enabled:
            try:
                loaded = [e for e in storage.load() if isinstance(e, dict) and isinstance(e.get("id"), str) and isinstance(e.get("ts"), int)]
                self._queue = loaded[-max_queue:]
            except Exception:
                pass
        self._thread: Optional[threading.Thread] = None
        if flush_interval_ms > 0:
            self._thread = threading.Thread(target=self._loop, args=(flush_interval_ms / 1000.0,), daemon=True, name="supermetry-apps")
            self._thread.start()

    # public API

    def set_enabled(self, on: bool) -> None:
        with self._lock:
            self._enabled = on
            if not on:
                self._queue = []
                self._persist()

    def start_session(self) -> None:
        with self._lock:
            if self._session_id:
                return
            self._session_id = secrets.token_hex(16)
        self._enqueue("session")

    def capture_exception(self, err: BaseException, handled: bool = True, level: Optional[str] = None, tags: Optional[dict] = None) -> None:
        """Sends the class name and scrubbed frames. The exception message is never read."""
        error_type = type(err).__name__
        frames = ""
        tb = getattr(err, "__traceback__", None)
        if tb is not None:
            frames = build_frames(frames_from_traceback(traceback.extract_tb(tb)), self.in_app)
        self._enqueue("error", error_type=error_type, frames=frames, handled=handled, level=level, tags=tags)

    def capture_crash(
        self,
        error_type: Optional[str] = None,
        exit_reason: Optional[str] = None,
        signal: Optional[str] = None,
        exit_code: Optional[int] = None,
        frames: Any = None,
        tags: Optional[dict] = None,
    ) -> None:
        self._enqueue("crash", error_type=error_type, exit_reason=exit_reason, signal=signal, exit_code=exit_code, frames=frames, tags=tags, handled=False, level="fatal")

    def capture_event(self, kind: str, **fields: Any) -> None:
        self._enqueue(kind, **fields)

    def track(self, name: str, props: Optional[Dict[str, str]] = None) -> None:
        """Records a custom product event.

        The name (1 to 64 characters of letters, digits and _.:$ - space, not starting
        with "$") and the install id are required: a call the server would reject is
        ignored. Props are strings. No user id is ever sent.
        """
        if not isinstance(name, str) or len(name) > 64 or name.startswith("$") or not _EVENT_NAME.fullmatch(name):
            return
        if not self.install_id:
            return
        self._enqueue("event", name=name, props=_clean_props(props))

    def screen(self, name: str, props: Optional[Dict[str, str]] = None) -> None:
        """Records a screen view (1 to 200 bytes). Needs the install id, like track()."""
        if not isinstance(name, str) or not name or not self.install_id:
            return
        self._enqueue("screen", screen=_clip_bytes(name, 200), props=_clean_props(props))

    def flush(self, timeout: float = 10.0) -> bool:
        """Sends everything queued. Returns True when the queue is empty, False if it timed out or sending stopped."""
        if not self._enabled:
            return True
        deadline = time.monotonic() + timeout
        if not self._drain_lock.acquire(timeout=max(0.0, timeout)):
            return False
        try:
            self._drain(deadline, True)
        finally:
            self._drain_lock.release()
        with self._lock:
            return not self._queue

    def shutdown(self, timeout: float = 10.0) -> None:
        self.flush(timeout)
        with self._lock:
            self._closed = True
            self._queue = []
        self._stop.set()

    # internals

    def _loop(self, interval: float) -> None:
        while not self._stop.wait(interval):
            if not self._enabled or time.monotonic() < self._next_attempt_at or not self._queue:
                continue
            if self._drain_lock.acquire(blocking=False):
                try:
                    self._drain(time.monotonic(), False)
                except Exception:
                    pass
                finally:
                    self._drain_lock.release()

    def _enqueue(self, kind: str, **f: Any) -> None:
        with self._lock:
            if not self._enabled or self._closed:
                return
            e: Dict[str, Any] = {
                "id": secrets.token_hex(16),
                "ts": self._now(),
                "kind": kind,
                "release": _clip(self.release, 64),
                "platform": self._plat["platform"],
                "arch": self._plat["arch"],
                "runtime": "python",
            }
            if self._plat.get("os_version"):
                e["os_version"] = _clip(str(self._plat["os_version"]), 32)
            if self.build:
                e["build"] = _clip(self.build, 64)
            if self.environment:
                e["environment"] = _clip(self.environment, 32)
            if self.install_id:
                e["install_id"] = _clip(self.install_id, 128)
            if self._session_id:
                e["session_id"] = self._session_id
            dc = f.get("device_class") or self.device_class
            if dc:
                e["device_class"] = _clip(dc, 32)
            if kind in ("screen", "event"):
                if f.get("screen"):
                    e["screen"] = f["screen"]
                if f.get("name"):
                    e["name"] = f["name"]
                if f.get("props"):
                    e["props"] = f["props"]
            elif kind != "session":
                e["level"] = f.get("level") or ("fatal" if kind == "crash" else "error")
                handled = f.get("handled")
                e["handled"] = (kind != "crash") if handled is None else bool(handled)
            if f.get("error_type"):
                e["error_type"] = _clip(f["error_type"], 64)
            if f.get("exit_reason"):
                e["exit_reason"] = f["exit_reason"]
            if f.get("signal"):
                e["signal"] = f["signal"]
            if isinstance(f.get("exit_code"), int):
                e["exit_code"] = f["exit_code"]
            frames = f.get("frames")
            if frames:
                text = "\n".join(frames) if isinstance(frames, (list, tuple)) else str(frames)
                scrubbed = scrub_frames(text)
                if scrubbed:
                    e["frames"] = scrubbed
            if f.get("fingerprint"):
                e["fingerprint"] = f["fingerprint"]
            if isinstance(f.get("occurrences"), int) and f["occurrences"] > 1:
                e["occurrences"] = f["occurrences"]
            tags = _clean_tags(f.get("tags"))
            if tags:
                e["tags"] = tags
            self._queue.append(e)
            while len(self._queue) > self.max_queue:
                self._drop([self._queue.pop(0)], "queue_full")
            self._persist()

    def _drop(self, events: List[dict], reason: str) -> None:
        if self.on_drop:
            try:
                self.on_drop(events, reason)
            except Exception:
                pass

    def _persist(self) -> None:
        if self.storage is not None:
            try:
                self.storage.save(list(self._queue))
            except Exception:
                pass

    def _next_batch(self) -> Optional[Tuple[List[dict], str]]:
        prefix = '{"site":' + json.dumps(self.app_id) + ',"events":['
        size = len((prefix + "]}").encode("utf-8"))
        picked: List[str] = []
        events: List[dict] = []
        for e in list(self._queue):
            if len(events) >= MAX_BATCH_EVENTS:
                break
            s = json.dumps(e, separators=(",", ":"))
            add = len(s.encode("utf-8")) + (1 if events else 0)
            if size + add > MAX_BODY_BYTES:
                if not events:
                    self._queue.remove(e)
                    self._drop([e], "too_large")
                    return self._next_batch()
                break
            size += add
            picked.append(s)
            events.append(e)
        if not events:
            return None
        return events, prefix + ",".join(picked) + "]}"

    def _backoff(self) -> float:
        base = min(BACKOFF_CAP_S, BACKOFF_BASE_S * 2 ** max(0, self._failures - 1))
        return min(BACKOFF_CAP_S, base * (1 + (self._random() - 0.5) * 0.5))

    def _drain(self, deadline: float, wait: bool) -> None:
        while True:
            with self._lock:
                if not self._enabled or not self._queue:
                    return
                cutoff = self._now() - self.max_age_days * DAY_MS
                old = [e for e in self._queue if e["ts"] < cutoff]
                if old:
                    self._queue = [e for e in self._queue if e["ts"] >= cutoff]
                    self._drop(old, "too_old")
                    self._persist()
                batch = self._next_batch()
            if batch is None:
                return
            events, body = batch
            ids = {e["id"] for e in events}
            status, reply = 0, ""
            try:
                status, reply = self._transport(
                    self.host + "/api/public/v1/app-events",
                    {"Content-Type": "application/json", "X-Supermetry-Key": self.ingest_key, "User-Agent": "supermetry-python/" + __version__},
                    body,
                )
            except Exception:
                status = 0

            def remove() -> None:
                with self._lock:
                    self._queue = [e for e in self._queue if e["id"] not in ids]
                    self._persist()

            if 200 <= status < 300:
                self._failures = 0
                remove()
                self._report_server_drops(events, reply)
            elif 400 <= status < 500 and status not in (408, 429):
                remove()
                self._drop(events, "http_%d" % status)
            else:
                self._failures += 1
                delay = self._backoff()
                self._next_attempt_at = time.monotonic() + delay
                if not wait or time.monotonic() + delay > deadline:
                    return
                self._sleep(delay)

    def _report_server_drops(self, events: List[dict], reply: str) -> None:
        if not reply:
            return
        try:
            for d in json.loads(reply).get("dropped", []):
                i = d.get("index")
                if isinstance(i, int) and 0 <= i < len(events):
                    self._drop([events[i]], str(d.get("reason")))
        except (ValueError, AttributeError):
            pass


_current: Optional[Client] = None


def init(**options: Any) -> Client:
    global _current
    if _current is not None:
        _current.shutdown(0)
    _current = Client(**options)
    return _current


def _need() -> Client:
    if _current is None:
        raise RuntimeError("supermetry: call init() first")
    return _current


def capture_exception(err: BaseException, **kw: Any) -> None:
    _need().capture_exception(err, **kw)


def capture_crash(**kw: Any) -> None:
    _need().capture_crash(**kw)


def capture_event(kind: str, **fields: Any) -> None:
    _need().capture_event(kind, **fields)


def track(name: str, props: Optional[Dict[str, str]] = None) -> None:
    _need().track(name, props)


def screen(name: str, props: Optional[Dict[str, str]] = None) -> None:
    _need().screen(name, props)


def start_session() -> None:
    _need().start_session()


def set_enabled(on: bool) -> None:
    _need().set_enabled(on)


def flush(timeout: float = 10.0) -> bool:
    return _need().flush(timeout)


def shutdown(timeout: float = 10.0) -> None:
    _need().shutdown(timeout)
