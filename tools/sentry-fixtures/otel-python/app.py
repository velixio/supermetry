"""OpenTelemetry Python exporter acceptance: the stock OTLP/HTTP span exporter
pointed at an app project with only the endpoint and one header changed.
Usage: SENTRY_FIXTURE_DSN=http://<key>@127.0.0.1:<port>/<site> python app.py
"""
import os
import time
from urllib.parse import urlparse

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.trace import SpanKind, Status, StatusCode

u = urlparse(os.environ["SENTRY_FIXTURE_DSN"])
endpoint = f"http://{u.hostname}:{u.port}/api/{u.path.lstrip('/')}/otlp/v1/traces"
provider = TracerProvider(resource=Resource.create({
    "service.name": "fixture-worker", "service.version": "1.0.0", "deployment.environment.name": "fixture"}))
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter(endpoint=endpoint, headers={"x-supermetry-key": u.username})))
tracer = provider.get_tracer("fixture")

with tracer.start_as_current_span("GET /api/items", kind=SpanKind.SERVER, attributes={
        "http.request.method": "GET", "http.route": "/api/items", "http.response.status_code": 200}):
    with tracer.start_as_current_span("SELECT items", kind=SpanKind.CLIENT, attributes={
            "db.system": "postgresql", "db.operation": "SELECT",
            "db.statement": "select * from items where owner = 'fixture' and id = 42"}):
        time.sleep(0.015)
    with tracer.start_as_current_span("GET", kind=SpanKind.CLIENT, attributes={
            "http.request.method": "GET", "http.response.status_code": 502,
            "url.full": "https://api.example.com/v1/items/12345?token=fixture-token"}) as s:
        time.sleep(0.01)
        s.set_status(Status(StatusCode.ERROR, "bad gateway"))
provider.shutdown()
