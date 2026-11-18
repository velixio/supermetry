# supermetry-apps (Python)

Crash and error reports from your Python app to [Supermetry App Health](../../docs/guide/apps.md). Standard library only, Python 3.9+. MIT licensed, small enough to vendor.

## Use

```python
import supermetry_apps as sm

sm.init(
    app_id="app_xxxxxxxx",
    ingest_key="smk_xxxxxxxx",
    host="https://app.supermetry.example",
    release="1.4.2",
    build="a1b2c3d",             # optional
    environment="production",    # optional
    install_id=my_random_id,     # optional, supplied by you
    enabled=user_consented,      # default True
)
sm.start_session()               # once per run: powers crash-free sessions

try:
    risky()
except Exception as err:
    sm.capture_exception(err, handled=True, tags={"op": "export"})

sm.flush(2.0)                    # before exit
```

`capture_crash(error_type=, exit_reason=, signal=, exit_code=, frames=, tags=)` sends `kind="crash"`, level `fatal`. `capture_event(kind, **fields)` sends `install_failed`, `incompatibility`, `operation_failed`, `hang` and the other kinds. `track(name, props=None)` records a custom product event and `screen(name, props=None)` a screen view, for the app's product analytics (active users, funnels, retention). Both need `install_id`, queue and batch like every other report, never send a user id, and ignore a name the server would reject: event names are 1 to 64 characters of letters, digits and `_ . : $ - ` and space, and must not start with `$`; props are strings (up to 20 keys, values up to 200 bytes, no `$` keys). `set_enabled(bool)`, `flush(timeout)` (returns `True` when the queue is empty) and `shutdown()` finish the API. `Client(...)` gives an independent instance with the same keyword options.

Options: `max_queue` (100), `flush_interval_ms` (5000, `0` disables the background thread), `max_age_days` (7, older events are discarded unsent), `in_app(path)` (first-party filter; the default drops `site-packages` and `<frozen ...>`), `on_drop(events, reason)`, `os_version`, `device_class`, `transport`, `storage`.

## Delivery

Events queue in memory (oldest dropped first when full) and go out in batches of at most 20 events and 16 KB, with header `X-Supermetry-Key` and body `{"site": app_id, "events": [...]}`. Each event has a random 32-hex `id`, reused on retry so the server can drop duplicates. Network errors, 429 and 5xx retry with exponential backoff (1 s, doubling, capped at 5 min, with jitter). 400 and 401 drop the batch and call `on_drop`. The User-Agent is `supermetry-python/<version>`.

## Surviving restarts (optional)

`JsonFileStorage(path)` keeps the unsent queue in a JSON file:

```python
sm.init(..., storage=sm.JsonFileStorage("/var/lib/myapp/supermetry-queue.json"))
```

Any object with `load()` and `save(events)` works.

## Privacy

- **The exception message is never sent.** Only the class name (`error_type`) and the stack frames leave the app. Frames are `path:line function` lines: first-party only, home folders replaced with `~/`, other absolute paths cut to their last 3 segments, at most 4 KB.
- No user names, file contents or environment variables are collected. Tag values are what you pass in, so keep personal data out of them.
- `install_id` is **supplied by your app**. If you don't pass one, the kit omits it and does not generate one. Use a random per-install id, not an account id or an email address.
- The kit sends nothing until it is enabled. Pass `enabled=False` at startup and call `set_enabled(True)` once the user agrees; `set_enabled(False)` clears the queue.

## Other platforms

Swift and Kotlin kits are not published yet. The wire format is plain JSON over HTTPS; see [docs/guide/apps.md](../../docs/guide/apps.md) and implement the same queue rules.

## Development

```sh
uv run --with pytest pytest
```
