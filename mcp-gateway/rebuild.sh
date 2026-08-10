#!/usr/bin/env bash
# Rebuild the WeKnora MCP gateway image in place (tag unchanged).
# Sources live in this directory; the local compose stack references them
# via build: /data/app/mcp/WeKnora/mcp-gateway.
set -euo pipefail

IMAGE="crpi-u265r07n4blchcqo.cn-shanghai.personal.cr.aliyuncs.com/ck-registry/weknora-mcp-gateway:latest"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

docker build -t "$IMAGE" "$DIR"
echo "rebuilt $IMAGE"
echo "apply with: cd deploy/weknore/local && docker compose up -d mcp-gateway"
