# Self-contained full build for the WeKnora MCP gateway.
# Clean base image keeps the layer count small and bounded (no incremental
# stacking on top of the previously published image, which used to grow
# the image by 2 layers per release — up to 98 layers, exceeding the
# overlay2 lowerdir limit on constrained hosts).
FROM python:3.11-slim

WORKDIR /app

# All Python deps in a single RUN: one layer, cached until requirements.txt changes.
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt \
    && pip install --no-cache-dir "pg8000>=1.30" \
    && rm -rf /root/.cache/pip

# Gateway sources + init_admin (used by the compose init-admin service).
COPY server.py knora_client.py init_admin.py /app/

EXPOSE 8000

CMD ["python", "server.py", "--host", "0.0.0.0", "--port", "8000"]