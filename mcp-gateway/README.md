# WeKnora MCP Gateway

A standalone MCP (Model Context Protocol) Gateway that provides
access to WeKnora knowledge bases over **Streamable HTTP** transport —
including full read/write Wiki page and folder management.
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

## Authentication (single layer, header-only)

Every `/mcp` request must carry the caller's WeKnora tenant API key:

```
X-API-Key: <weknora tenant api key>              # authenticates + selects the WeKnora tenant
```

- The gateway pre-validates the key at **session establishment** (backend
  probe, short TTL cache) — invalid keys are rejected with
  `401 {"error":"unauthorized"}` before any `initialize` / `tools/list`.
- Missing `X-API-Key` on any `/mcp` request → `401 {"error":"missing X-API-Key header"}`
- Data access is still enforced by the WeKnora backend on **every** request:
  the tenant API key needs at least the `retrieve` capability for read tools;
  Wiki **write** tools additionally require "write" / owner capability on the KB.
- The former gateway-level `Authorization: Bearer <MCP_GATEWAY_AUTH_TOKEN>`
  shared secret has been removed — clients must send **only** `X-API-Key`.

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

The following Wiki tools extend the gateway with **full read/write** access to a
knowledge base's Wiki knowledge base (pages, folders, graph, link maintenance,
logs, and issues). All are KB-scoped via `kb_id`; **write** operations require
the session API key to have "write" / owner capability on the KB
(guarded by `OwnedWikiKBOrAdmin + KBAccessWrite`).

### Wiki page management

| Tool | Access | Required args | Description |
|---|---|---|---|
| `create_wiki_page` | write | `kb_id`, `slug`, `title`, `content` | Create a wiki page (also accepts `summary`, `page_type`, `aliases`, `source_refs`, `folder_id`) |
| `update_wiki_page` | write | `kb_id`, `slug` | Update an existing wiki page by slug (pass only fields to change) |
| `delete_wiki_page` | write | `kb_id`, `slug` | Soft-delete a wiki page |
| `move_wiki_page` | write | `kb_id`, `slug` | Move a page into a folder (`folder_id`, empty = root) |
| `list_wiki_pages` | read | `kb_id` | Paginated list of wiki pages |

### Wiki folder management

| Tool | Access | Required args | Description |
|---|---|---|---|
| `list_wiki_folders` | read | `kb_id` | List child folders of `parent_id` (empty = root) |
| `create_wiki_folder` | write | `kb_id`, `name` | Create a new empty folder |
| `update_wiki_folder` | write | `kb_id`, `folder_id` | Rename and/or reparent (`name`, `parent_id`, `move_parent`) |
| `delete_wiki_folder` | write | `kb_id`, `folder_id` | Delete an empty folder |

### Wiki graph / stats / index / log

| Tool | Access | Required args | Description |
|---|---|---|---|
| `wiki_graph` | read | `kb_id` | Wiki link graph (`mode` overview/ego, `center`, `depth`, `limit`, `types`) |
| `wiki_stats` | read | `kb_id` | Aggregate wiki statistics |
| `wiki_index_view` | read | `kb_id` | Cursor-paginated wiki index view |
| `wiki_log` | read | `kb_id` | Newest-first operation log (`cursor`, `limit`) |

### Wiki link maintenance & issues

| Tool | Access | Required args | Description |
|---|---|---|---|
| `wiki_rebuild_links` | write | `kb_id` | Re-parse all pages and rebuild link references |
| `wiki_lint` | read | `kb_id` | Wiki health check report |
| `wiki_auto_fix` | write | `kb_id` | Auto-fix fixable issues (broken links, etc.) |
| `wiki_list_issues` | read | `kb_id` | List wiki page issues (`slug`, `status` filters) |
| `wiki_update_issue_status` | write | `kb_id`, `issue_id`, `status` | Set issue status: `pending` / `ignored` / `resolved` |

> Note: DELETE operations return HTTP 204 — the client maps an empty response
> to `{"success": true}` so callers don't hit a JSON-decoding error.

## Configuration

| Env var | Required | Default | Description |
|---|---|---|---|
| `WEKNORA_BASE_URL` | ✅ | `http://localhost:8080/api/v1` | Go backend URL |
| `MCP_HOST` | ❌ | `0.0.0.0` | Gateway listen address |
| `MCP_PORT` | ❌ | `8000` | Gateway listen port |
| `WEKNORA_VERIFY_SSL` | ❌ | `true` | Set to `false` to disable SSL verification |

> `MCP_GATEWAY_AUTH_TOKEN` was removed: the gateway no longer uses a shared
> Bearer secret — clients authenticate with `X-API-Key` only.
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
