# Test stand-in for an app image: same entrypoint shape as VoiceStudio.
FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir fastapi==0.115.12 uvicorn==0.34.2
COPY backend backend
ENTRYPOINT ["python3", "backend/docker_entrypoint.py"]
