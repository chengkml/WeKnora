# WeKnora MCP Gateway

A standalone MCP (Model Context Protocol) Gateway that provides
read-only access to WeKnora knowledge bases over **Streamable HTTP** transport.
Runs as a **separate container** from the WeKnora Go backend.

## Architecture

```
                          ┌──────────────────────┐
MCP Client ──Streamable──►│  weknora-mcp-gateway  │──REST──► WeKnora Go API
   HTTP (single /mcp)     │  (single Python proc) │          (:8080/api/v1)
                          └──────────────────────┘
```

- **Single process** — Python asyncio (Starlette + uvicorn) with
  `StreamableHTTPSessionManager` handles all MCP sessions.
- **No database** — per-key REST clients are cached in memory only.
- **Sessions are stateful** (`stateless=False`) — the tenant key is bound to
  the session's server-run task at `initialize`; responses use SSE streams
  inside the single endpoint (`json_response=False`), compatible with
  agno/mcp clients.
- **Tenant isolation via `X-API-Key` header** — every `/mcp` request carries
  the caller's WeKnora tenant API key; one gateway process serves every tenant.
- **KB scoping via tool arguments** — KB tools take an explicit `kb_id`
  (call `list_knowledge_bases` first). The URL no longer contains a KB id.

## Endpoints

| URL | Purpose |
|---|---|
| `/mcp` | Streamable HTTP MCP endpoint (POST messages / GET SSE stream / DELETE session) |
| `GET /health` | Liveness probe |

> The legacy SSE transport (`GET /mcp/sse` + `POST /mcp/messages`) was removed
> in 2026-07; clients must use `transport: "streamable-http"` against `/mcp`.

## Authentication (two layers, both header-only)

Every request must carry **both** headers:

```
Authorization: Bearer <MCP_GATEWAY_AUTH_TOKEN>   # gateway-level shared secret
X-API-Key: <weknora tenant api key>              # selects the WeKnora tenant
```

- Missing/ wrong Bearer → `401 {"error":"unauthorized"}`
- Missing `X-API-Key` on any `/mcp` request → `401 {"error":"missing X-API-Key header"}`
- The tenant API key needs at least the `retrieve` capability.

## Tools

All tools are **read-only**:

| Tool | Required args | Description |
|---|---|---|
| `list_knowledge_bases` | — | List every KB accessible with the session key — call first |
| `hybrid_search` | `kb_id`, `query` | Semantic + keyword search |
| `list_documents` | `kb_id` | List documents with pagination |
| `get_document` | `knowledge_id` | Get document metadata by UUID |
| `list_chunks` | `knowledge_id` | List text chunks of a document |
| `wiki_search` | `kb_id`, `query` | Full-text wiki search |
| `wiki_read_page` | `kb_id`, `slug` | Read a wiki page by slug |

## Configuration

| Env var | Required | Default | Description |
|---|---|---|---|
| `WEKNORA_BASE_URL` | ✅ | `http://localhost:8080/api/v1` | Go backend URL |
| `MCP_GATEWAY_AUTH_TOKEN` | ✅ | — | Shared secret for MCP clients |
| `MCP_HOST` | ❌ | `0.0.0.0` | Gateway listen address |
| `MCP_PORT` | ❌ | `8000` | Gateway listen port |
| `WEKNORA_VERIFY_SSL` | ❌ | `true` | Set to `false` to disable SSL verification |

> `WEKNORA_API_KEY` was removed: the gateway no longer holds a global key.

## Build & Run

```bash
./rebuild.sh            # incremental build on top of the published image, tag unchanged
```

The image also carries `init_admin.py` (see below); the default command is
unchanged (`python server.py`).

### Local development (without Docker)

```bash
pip install -r requirements.txt
WEKNORA_BASE_URL=http://localhost:8080/api/v1 \
  MCP_GATEWAY_AUTH_TOKEN=dev-secret \
  python server.py
```

### Client configuration

```json
{
  "mcpServers": {
    "weknora": {
      "url": "http://gateway:8000/mcp",
      "transport": "streamable-http",
      "headers": {
        "Authorization": "Bearer <gateway-token>",
        "X-API-Key": "<tenant api key>"
      }
    }
  }
}
```

## init_admin.py — super-user bootstrap

One-shot helper shipped in the same image, run as the compose `init-admin`
service. Creates the agreed super user and promotes it to SystemAdmin
(idempotent, no WeKnora restart needed). This has taken over the role of the
removed Synapse-side wiki/bootstrap control plane.

1. Waits for `WEKNORA_BASE_URL/health`
2. `POST /api/v1/auth/register` (skips if the user exists)
3. Direct SQL `UPDATE users SET is_system_admin=true` (via pg8000)

Required env: `WEKNORA_BASE_URL` (no `/api/v1` suffix),
`WEKNORA_ADMIN_USERNAME` / `WEKNORA_ADMIN_EMAIL` / `WEKNORA_ADMIN_PASSWORD`,
`DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASSWORD` / `DB_NAME`.
Re-run with `docker compose run --rm init-admin`.

## Notes

- The gateway does **not** modify any existing WeKnora code.
- Streamable HTTP sessions hold a server-run task per `initialize`; plan your
  file-descriptor limits accordingly.
