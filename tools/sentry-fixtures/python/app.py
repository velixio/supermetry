"""Usage: python app.py <case>
Generator for sentry-sdk fixtures. Only init plus real behaviour; no manual
capture calls. Cases: unhandled-exception, logging-error, wsgi-sessions,
client-report, transaction. Set SENTRY_FIXTURE_COMPRESSION=br to send brotli bodies
(requires the brotli package; default is gzip).
"""
import logging
import os
import sys

import sentry_sdk
from sentry_sdk.integrations.wsgi import SentryWsgiMiddleware

case = sys.argv[1]
kwargs = dict(
    dsn=os.environ["SENTRY_FIXTURE_DSN"],
    release="fixture@1.0.0",
    environment="fixture",
    server_name="fixture-host",
    traces_sample_rate=1.0 if case == "transaction" else 0,
    include_local_variables=False,  # locals embed this machine's stdlib paths
)
# brotli is installed in this venv (the SDK would pick it by default); force the
# usual gzip body unless a br fixture is asked for.
kwargs["_experiments"] = {"transport_compression_algo": os.environ.get("SENTRY_FIXTURE_COMPRESSION", "gzip")}
if case == "client-report":
    kwargs["before_send"] = lambda event, hint: None
sentry_sdk.init(**kwargs)


def app(environ, start_response):
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"ok"]


if case == "unhandled-exception":
    # sys.excepthook is patched by the default integration; the atexit hook flushes
    def boom():
        raise ValueError("fixture unhandled exception")

    boom()
elif case == "logging-error":
    logging.getLogger("fixture").error("fixture logging error", exc_info=False)
elif case == "wsgi-sessions":
    wrapped = SentryWsgiMiddleware(app)
    from wsgiref.util import setup_testing_defaults

    for _ in range(3):
        env = {}
        setup_testing_defaults(env)
        b"".join(wrapped(env, lambda *a: None))
elif case == "transaction":
    with sentry_sdk.start_transaction(name="GET /api/items", op="http.server", source="route"):
        with sentry_sdk.start_span(op="db.query", name="select * from items where owner = 'fixture' and id = 42"):
            pass
        with sentry_sdk.start_span(op="http.client", name="GET https://api.example.com/v1/items/12345?token=fixture"):
            pass
elif case == "client-report":
    raise RuntimeError("fixture dropped")  # unhandled; before_send drops it
else:
    sys.exit(f"unknown case {case}")

sentry_sdk.flush(5)
sentry_sdk.get_client().close(timeout=5)
