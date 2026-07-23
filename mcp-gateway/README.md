# WeKnora MCP Gateway

A standalone, stateless MCP (Model Context Protocol) Gateway that provides
KB-scoped read-only access to WeKnora knowledge bases over SSE transport.
Runs as a **separate container** from the WeKnora Go backend.

## Architecture

```
                     ┌──────────────────────┐
MCP Client ──SSE──►  │  weknora-mcp-gateway  │──REST──► WeKnora Go API
(Claude Desktop,     │  (single Python proc) │          (:8080/api/v1)
etc.)                └──────────────────────┘
```

- **Single process** — Python asyncio (Starlette + uvicorn) handles all SSE connections.
- **Stateless** — no cache, no database; every tool call hits the Go API in real time.
- **KB isolation via URL path** — connect to different URLs to access different KBs.

## Endpoints

| URL | Scope |
|---|---|
| `/mcp/<kb-uuid>/sse` | Single knowledge base |
| `/mcp/__all__/sse` | All knowledge bases the API key can access |

Each SSE connection binds the knowledge-base ID from the URL; every tool call
is automatically scoped to that KB.

### Message endpoint

The MCP protocol uses a secondary POST endpoint for client-to-server messages.
The SSE stream advertises its location automatically; **you do not need to
configure it manually**.

In the path structure the messages endpoint is at:

| SSE endpoint | Messages endpoint (auto‑resolved) |
|---|---|
| `/mcp/<kb-uuid>/sse` | `/mcp/<kb-uuid>/messages` |
| `/mcp/__all__/sse` | `/mcp/__all__/messages` |

## Tools

All tools are **read-only**:

| Tool | Description | KB scoped |
|---|---|---|
| `hybrid_search` | Semantic + keyword search | ✅ |
| `list_documents` | List documents with pagination | ✅ |
| `get_document` | Get document metadata by UUID | ✅ (implicitly through KB) |
| `list_chunks` | List text chunks of a document | ✅ (implicitly through KB) |
| `wiki_search` | Full‑text wiki search | ✅ |
| `wiki_read_page` | Read a wiki page by slug | ✅ |
| `list_knowledge_bases` | List KB(s) visible to the session | ✅ |

## Authentication

The gateway requires a shared secret passed via the `MCP_GATEWAY_AUTH_TOKEN`
environment variable.  Every request must include:

```
Authorization: Bearer <token>
```

## Configuration

| Env var | Required | Default | Description |
|---|---|---|---|
| `WEKNORA_BASE_URL` | ✅ | `http://localhost:8080/api/v1` | Go backend URL |
| `WEKNORA_API_KEY` | ✅ | — | API key for Go backend |
| `MCP_GATEWAY_AUTH_TOKEN` | ✅ | — | Shared secret for MCP clients |
| `MCP_HOST` | ❌ | `0.0.0.0` | Gateway listen address |
| `MCP_PORT` | ❌ | `8000` | Gateway listen port |
| `WEKNORA_VERIFY_SSL` | ❌ | `true` | Set to `false` to disable SSL verification |

## Quick Start

### Build

```bash
docker build -t weknora-mcp-gateway ./mcp-gateway
```

### Run

```bash
docker run -d --name weknora-mcp \
  -p 8000:8000 \
  -e WEKNORA_BASE_URL=http://weknora-go:8080/api/v1 \
  -e WEKNORA_API_KEY=your-api-key \
  -e MCP_GATEWAY_AUTH_TOKEN=your-gateway-secret \
  weknora-mcp-gateway
```

### Local development (without Docker)

```bash
cd mcp-gateway
pip install -r requirements.txt
WEKNORA_BASE_URL=http://localhost:8080/api/v1 \
  WEKNORA_API_KEY=xxx \
  MCP_GATEWAY_AUTH_TOKEN=dev-secret \
  python server.py
```

### Claude Desktop configuration

```json
{
  "mcpServers": {
    "weknora-products": {
      "url": "http://gateway:8000/mcp/<kb-uuid>/sse"
    }
  }
}
```

Replace `<kb-uuid>` with the knowledge-base UUID from your WeKnora instance.
Use `list_knowledge_bases` (via `__all__`) to discover available KB UUIDs.

## Relationship with `mcp-server/`

The existing `mcp-server/` directory contains a full‑featured MCP server with
read/write tools and stdio transport, intended for local development and
debugging.  The **mcp-gateway** is a lightweight, production-oriented component
that:

- Runs as a **separate container** (not alongside the Go process)
- Exposes **read-only tools** only
- Uses **SSE transport** instead of stdio
- Provides **KB-level isolation** via URL paths

Both can coexist; which one you use depends on the deployment scenario.

## Notes

- The gateway does **not** modify any existing code — `mcp-server/` and the
  Go backend are left untouched.
- Each SSE connection is a long‑lived HTTP connection.  Plan your connection
  pool and file‑descriptor limits accordingly.
- Use the `WEKNORA_API_KEY` with at least `retrieve` scope.
