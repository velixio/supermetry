# Wraps an existing Python image (a FastAPI/Flask/Django app) so it runs under
# OpenTelemetry auto-instrumentation. The app's source and base image are not
# changed; build this on top of the built image:
#
#   docker build --build-arg BASE_IMAGE=<your image> -t <your image>-otel .
#
# Settings are environment variables, see compose.override.yml.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}

# The HTTP/protobuf exporter only: no gRPC stack, so no clash with the app's own
# grpcio/protobuf pins. PIP_BREAK_SYSTEM_PACKAGES covers a base image whose
# python3 is distro-managed (Ubuntu 24.04); it is harmless on python:slim.
ENV PIP_BREAK_SYSTEM_PACKAGES=1
RUN python3 -m pip install --no-cache-dir \
        opentelemetry-distro==0.66b1 \
        opentelemetry-exporter-otlp-proto-http==1.45.1 \
    && opentelemetry-bootstrap -a install

# Same command as the base image's ENTRYPOINT, behind the instrumenting launcher.
# (VoiceStudio: deploy/Dockerfile). Edit the tail if your image differs.
ENTRYPOINT ["opentelemetry-instrument", "python3", "backend/docker_entrypoint.py"]
