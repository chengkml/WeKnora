# Incremental rebuild on top of the published gateway image.
# Base image already carries tested dependency versions (mcp/starlette/uvicorn);
# we only add psycopg for init_admin.py and overlay the new sources.
FROM crpi-u265r07n4blchcqo.cn-shanghai.personal.cr.aliyuncs.com/ck-registry/weknora-mcp-gateway:latest

RUN pip install --no-cache-dir "pg8000>=1.30"

COPY server.py knora_client.py init_admin.py /app/

# Default command inherited from the base image (python server.py).
# The compose init-admin service overrides it with: python /app/init_admin.py
