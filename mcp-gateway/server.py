#!/usr/bin/env python3
"""
WeKnora MCP Gateway — multi-tenant Streamable HTTP MCP server (per-user API key edition).

Single Starlette process exposing ONE MCP endpoint:

    /mcp            → Streamable HTTP（POST/GET/DELETE 单端点双向）

Single-layer auth (header-only):

    X-API-Key: <weknora tenant api key>              — authenticates + selects the WeKnora tenant

The X-API-Key is pre-validated at session establishment (backend probe +
short TTL cache) so invalid keys never reach the tool surface; data access
is still enforced by the backend on every request. The key is bound to the
MCP session via contextvars: the session's server-run task is created on the
initialize request and inherits the context (task creation copies
contextvars), so every tool call in that session resolves (or creates) a
WeKnoraGatewayClient for that key — one gateway process serves every tenant.
Knowledge-base scoping is explicit: KB tools take a required ``kb_id``
argument (call ``list_knowledge_bases`` first).
"""

from __future__ import annotations

import asyncio
import contextvars
import hashlib
import logging
import os
import threading
import time
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Any

import mcp.types as types
import uvicorn
from mcp.server import Server
from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
from starlette.applications import Starlette
from starlette.responses import JSONResponse
from starlette.routing import Mount, Route

from knora_client import WeKnoraGatewayClient, probe_api_key

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("mcp-gateway")

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

WEKNORA_BASE_URL = os.getenv("WEKNORA_BASE_URL", "http://localhost:8080/api/v1")


# ---------------------------------------------------------------------------
# Per-connection WeKnora API key via contextvars
# ---------------------------------------------------------------------------

_api_key_context: contextvars.ContextVar[str] = contextvars.ContextVar(
    "weknora_api_key"
)


def current_api_key() -> str:
    """Return the WeKnora API key bound to the current MCP session."""
    return _api_key_context.get()


# ---------------------------------------------------------------------------
# Per-key client cache (clients are stateless — safe to share per key)
# ---------------------------------------------------------------------------

_client_cache: dict[str, WeKnoraGatewayClient] = {}
_client_lock = threading.Lock()


def current_client() -> WeKnoraGatewayClient:
    """Resolve (or create) the WeKnora client for the session's API key."""
    api_key = current_api_key()
    with _client_lock:
        client = _client_cache.get(api_key)
        if client is None:
            client = WeKnoraGatewayClient(WEKNORA_BASE_URL, api_key)
            _client_cache[api_key] = client
        return client


# ---------------------------------------------------------------------------
# Session-establishment key validation (backend probe + TTL cache)
# ---------------------------------------------------------------------------

# Cache keyed by sha256(api_key). A TTL miss only affects whether a NEW session
# may be established; per-request data access is still enforced by the backend,
# so a revoked key stops working immediately for existing sessions regardless
# of this cache.
_valid_cache: dict[str, tuple[bool, float]] = {}
_valid_cache_lock = threading.Lock()
_VALID_TTL = 300  # 秒


async def _key_valid(api_key: str) -> bool:
    """Return whether the API key may establish a session."""
    key_id = hashlib.sha256(api_key.encode()).hexdigest()
    now = time.time()
    with _valid_cache_lock:
        hit = _valid_cache.get(key_id)
        if hit and hit[1] > now:
            return hit[0]

    # Probe off the event loop — knora_client is synchronous and a blocking
    # request here would stall every session on this single-process gateway.
    ok = await asyncio.to_thread(_probe_key, api_key)

    with _valid_cache_lock:
        _valid_cache[key_id] = (ok, now + _VALID_TTL)
    if not ok:
        logger.warning("Rejected session for invalid/insufficient API key")
    return ok


def _probe_key(api_key: str) -> bool:
    """Validate a key against the backend (200=valid, 401/403=rejected).

    Runs on a worker thread via :func:`_key_valid`; any non-200 result is
    fail-closed so a briefly-unavailable backend never grants access.
    """
    return probe_api_key(WEKNORA_BASE_URL, api_key)


# ---------------------------------------------------------------------------
# MCP Server
# ---------------------------------------------------------------------------

mcp_server = Server("weknora-mcp-gateway")

_KB_ID_PROP = {
    "kb_id": {
        "type": "string",
        "description": "Knowledge base UUID. Call list_knowledge_bases first "
        "to discover accessible IDs.",
    }
}


@mcp_server.list_tools()
async def handle_list_tools() -> list[types.Tool]:
    """Expose read-only tools. KB-scoped tools take an explicit kb_id."""
    return [
        types.Tool(
            name="list_knowledge_bases",
            description="List every knowledge base accessible with this session's "
            "API key. Call this first to discover kb_id values for the other tools.",
            inputSchema={"type": "object", "properties": {}},
        ),
        types.Tool(
            name="hybrid_search",
            description="Hybrid (semantic + keyword) search within one knowledge "
            "base. Requires kb_id from list_knowledge_bases. Results include "
            "document text, metadata, and relevance scores.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "query": {
                        "type": "string",
                        "description": "Natural-language search query",
                    },
                    "vector_threshold": {
                        "type": "number",
                        "description": "Vector similarity threshold (0-1)",
                        "default": 0.5,
                    },
                    "keyword_threshold": {
                        "type": "number",
                        "description": "Keyword match threshold (0-1)",
                        "default": 0.3,
                    },
                    "match_count": {
                        "type": "integer",
                        "description": "Max results to return",
                        "default": 5,
                    },
                },
                "required": ["kb_id", "query"],
            },
        ),
        types.Tool(
            name="list_documents",
            description="List documents in one knowledge base with pagination. "
            "Requires kb_id from list_knowledge_bases.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "page": {
                        "type": "integer",
                        "description": "Page number (1-based)",
                        "default": 1,
                    },
                    "page_size": {
                        "type": "integer",
                        "description": "Items per page",
                        "default": 20,
                    },
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="get_document",
            description="Get metadata for a single document by its UUID.",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {
                        "type": "string",
                        "description": "Document UUID",
                    }
                },
                "required": ["knowledge_id"],
            },
        ),
        types.Tool(
            name="list_chunks",
            description="List text chunks of a document with pagination.",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {
                        "type": "string",
                        "description": "Document UUID",
                    },
                    "page": {
                        "type": "integer",
                        "description": "Page number (1-based)",
                        "default": 1,
                    },
                    "page_size": {
                        "type": "integer",
                        "description": "Items per page",
                        "default": 20,
                    },
                },
                "required": ["knowledge_id"],
            },
        ),
        types.Tool(
            name="wiki_search",
            description="Search wiki pages within one knowledge base. "
            "Requires kb_id from list_knowledge_bases.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "query": {
                        "type": "string",
                        "description": "Full-text search query",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Max results",
                        "default": 10,
                    },
                },
                "required": ["kb_id", "query"],
            },
        ),
        types.Tool(
            name="wiki_read_page",
            description="Read a wiki page by its slug within one knowledge base. "
            "Returns markdown content, metadata, and linked pages.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "slug": {
                        "type": "string",
                        "description": "Wiki page slug (URL path)",
                    },
                },
                "required": ["kb_id", "slug"],
            },
        ),
        # --- Wiki page management ---
        types.Tool(
            name="create_wiki_page",
            description="Create a new wiki page in the knowledge base. Requires "
            "\"write\" capability on the API key. Pass page fields (slug, title, "
            "content, etc.).",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "slug": {"type": "string", "description": "URL-friendly page slug"},
                    "title": {"type": "string", "description": "Human-readable title"},
                    "summary": {"type": "string", "description": "One-line summary"},
                    "content": {"type": "string", "description": "Full markdown content"},
                    "page_type": {
                        "type": "string",
                        "description": "summary | entity | concept | synthesis "
                        "| comparison (default entity)",
                    },
                    "aliases": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Alternate names / abbreviations",
                    },
                    "source_refs": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Source knowledge ID references",
                    },
                    "folder_id": {
                        "type": "string",
                        "description": "Primary folder to file the page under (empty = root)",
                    },
                    "folder_ids": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Full set of folders to mount the page under "
                        "(overrides folder_id; must include the primary folder)",
                    },
                },
                "required": ["kb_id", "slug", "title", "content"],
            },
        ),
        types.Tool(
            name="update_wiki_page",
            description="Update an existing wiki page (by slug) with new fields. "
            "\"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "slug": {"type": "string", "description": "Wiki page slug (URL path)"},
                    "title": {"type": "string", "description": "Human-readable title"},
                    "summary": {"type": "string", "description": "One-line summary"},
                    "content": {"type": "string", "description": "Full markdown content"},
                    "page_type": {
                        "type": "string",
                        "description": "summary | entity | concept | synthesis | comparison",
                    },
                    "aliases": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Alternate names / abbreviations",
                    },
                    "source_refs": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Source knowledge ID references",
                    },
                    "folder_id": {
                        "type": "string",
                        "description": "Primary folder to file the page under (empty = root)",
                    },
                    "folder_ids": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Full set of folders to mount the page under "
                        "(overrides folder_id; must include the primary folder)",
                    },
                },
                "required": ["kb_id", "slug"],
            },
        ),
        types.Tool(
            name="delete_wiki_page",
            description="Soft-delete a wiki page by slug. \"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {**_KB_ID_PROP, "slug": {"type": "string"}},
                "required": ["kb_id", "slug"],
            },
        ),
        types.Tool(
            name="move_wiki_page",
            description="Relocate a wiki page into a folder (folder_id \"\" = root). "
            "\"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "slug": {"type": "string", "description": "Page slug to move"},
                    "folder_id": {
                        "type": "string",
                        "description": "Destination folder_id (empty = root)",
                    },
                },
                "required": ["kb_id", "slug"],
            },
        ),
        types.Tool(
            name="list_wiki_pages",
            description="Paginated list of wiki pages in a knowledge base.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "page": {"type": "integer", "default": 1},
                    "page_size": {"type": "integer", "default": 20},
                },
                "required": ["kb_id"],
            },
        ),
        # --- Wiki folder management ---
        types.Tool(
            name="list_wiki_folders",
            description="List direct child folders of a parent folder "
            "(empty parent_id = root level). Empty folders (no pages "
            "underneath) are included.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "parent_id": {
                        "type": "string",
                        "description": "Parent folder id (empty = root)",
                    },
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="create_wiki_folder",
            description="Create a new empty wiki folder. \"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "name": {"type": "string", "description": "Folder name"},
                    "parent_id": {
                        "type": "string",
                        "description": "Parent folder id (empty = root)",
                    },
                },
                "required": ["kb_id", "name"],
            },
        ),
        types.Tool(
            name="update_wiki_folder",
            description="Rename and/or reparent a wiki folder. Set move_parent to "
            "true to apply a new parent_id. \"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "folder_id": {"type": "string", "description": "Folder to update"},
                    "name": {"type": "string", "description": "New folder name"},
                    "parent_id": {
                        "type": "string",
                        "description": "New parent folder id (empty = root)",
                    },
                    "move_parent": {
                        "type": "boolean",
                        "description": "Apply parent_id only when true",
                    },
                },
                "required": ["kb_id", "folder_id"],
            },
        ),
        types.Tool(
            name="delete_wiki_folder",
            description="Delete an empty wiki folder (no pages, no child folders). "
            "\"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "folder_id": {"type": "string", "description": "Folder to delete"},
                },
                "required": ["kb_id", "folder_id"],
            },
        ),
        # --- Wiki graph / stats / index ---
        types.Tool(
            name="wiki_graph",
            description="Return a slice of the wiki link graph for visualization. "
            "mode=overview (default) returns the most-connected pages; mode=ego "
            "returns the neighborhood of a center slug (center required in ego mode).",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "mode": {
                        "type": "string",
                        "description": "overview (default) | ego",
                        "default": "overview",
                    },
                    "center": {
                        "type": "string",
                        "description": "Center slug for ego mode",
                    },
                    "depth": {
                        "type": "integer",
                        "description": "Ego BFS depth (1-3, default 1)",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Max nodes (default 500, max 2000)",
                    },
                    "types": {
                        "type": "string",
                        "description": "Comma-separated page_type allow-list",
                    },
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="wiki_stats",
            description="Return aggregate statistics about the wiki.",
            inputSchema={
                "type": "object",
                "properties": {**_KB_ID_PROP},
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="wiki_index_view",
            description="Get the wiki index view (cursor-paginated directory listing "
            "of pages).",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "limit": {"type": "integer", "default": 50},
                    "types": {
                        "type": "string",
                        "description": "Comma-separated page_type allow-list",
                    },
                },
                "required": ["kb_id"],
            },
        ),
        # --- Wiki link maintenance ---
        types.Tool(
            name="wiki_rebuild_links",
            description="Re-parse all pages and rebuild bidirectional link "
            "references. \"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {**_KB_ID_PROP},
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="wiki_lint",
            description="Run a comprehensive health check over the wiki.",
            inputSchema={
                "type": "object",
                "properties": {**_KB_ID_PROP},
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="wiki_auto_fix",
            description="Automatically fix fixable wiki issues (broken links, etc.). "
            "\"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {**_KB_ID_PROP},
                "required": ["kb_id"],
            },
        ),
        # --- Wiki log / issues ---
        types.Tool(
            name="wiki_log",
            description="Get a paginated feed of wiki operation events "
            "(newest-first, cursor-paginated).",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "limit": {"type": "integer", "default": 50},
                    "cursor": {"type": "string", "description": "Opaque next_cursor"},
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="wiki_list_issues",
            description="List issues flagged on wiki pages (optionally filtered by "
            "slug or status).",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "slug": {"type": "string", "description": "Filter by page slug"},
                    "status": {
                        "type": "string",
                        "description": "Filter by status (pending | ignored | resolved)",
                    },
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="wiki_update_issue_status",
            description="Set a wiki issue status: pending | ignored | resolved. "
            "\"write\" capability required.",
            inputSchema={
                "type": "object",
                "properties": {
                    **_KB_ID_PROP,
                    "issue_id": {"type": "string", "description": "Issue UUID"},
                    "status": {
                        "type": "string",
                        "description": "pending | ignored | resolved",
                    },
                },
                "required": ["kb_id", "issue_id", "status"],
            },
        ),
        types.Tool(
            name="tokenize",
            description="Tokenize text using the WeKnora tokenizer. Returns words "
            "and the token count.",
            inputSchema={
                "type": "object",
                "properties": {
                    "text": {"type": "string", "description": "Text to tokenize"},
                    "mode": {
                        "type": "string",
                        "description": "cut (default) | cut_for_search",
                        "default": "cut",
                    },
                    "stopwords": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Optional stopwords to remove",
                    },
                },
                "required": ["text"],
            },
        ),
    ]


@mcp_server.call_tool()
async def handle_call_tool(
    name: str, arguments: dict[str, Any] | None
) -> list[types.TextContent]:
    """Dispatch tool calls through the session-keyed WeKnora client."""
    args = arguments or {}

    try:
        client = current_client()
    except LookupError:
        return [
            types.TextContent(
                type="text",
                text="Error: no WeKnora API key bound to this session "
                "(connect with an X-API-Key header).",
            )
        ]

    try:
        if name == "list_knowledge_bases":
            result = client.list_knowledge_bases()
        elif name == "hybrid_search":
            config = {
                k: args[k]
                for k in ("vector_threshold", "keyword_threshold", "match_count")
                if k in args
            }
            result = client.hybrid_search(
                args["kb_id"], args["query"], config or None
            )
        elif name == "list_documents":
            result = client.list_knowledge(
                args["kb_id"],
                page=args.get("page", 1),
                page_size=args.get("page_size", 20),
            )
        elif name == "get_document":
            result = client.get_knowledge(args["knowledge_id"])
        elif name == "list_chunks":
            result = client.list_chunks(
                args["knowledge_id"],
                page=args.get("page", 1),
                page_size=args.get("page_size", 20),
            )
        elif name == "wiki_search":
            result = client.wiki_search(
                args["kb_id"],
                query=args["query"],
                limit=args.get("limit", 10),
            )
        elif name == "wiki_read_page":
            result = client.wiki_read_page(args["kb_id"], args["slug"])
        elif name == "create_wiki_page":
            page = {
                "slug": args["slug"],
                "title": args["title"],
                "content": args["content"],
                "summary": args.get("summary", ""),
                "page_type": args.get("page_type", "entity"),
                "aliases": args.get("aliases", []),
                "source_refs": args.get("source_refs", []),
            }
            if "folder_ids" in args:
                page["folder_ids"] = args["folder_ids"]
            page["folder_id"] = args.get("folder_id", "")
            result = client.create_wiki_page(args["kb_id"], page)
        elif name == "update_wiki_page":
            # Only forward fields the caller actually passed.
            page = {}
            for f in (
                "title", "summary", "content", "page_type",
                "aliases", "source_refs", "folder_id", "folder_ids",
            ):
                if f in args:
                    page[f] = args[f]
            result = client.update_wiki_page(
                args["kb_id"], args["slug"], page
            )
        elif name == "delete_wiki_page":
            result = client.delete_wiki_page(args["kb_id"], args["slug"])
        elif name == "move_wiki_page":
            result = client.move_wiki_page(
                args["kb_id"], args["slug"], args.get("folder_id", "")
            )
        elif name == "list_wiki_pages":
            result = client.list_wiki_pages(
                args["kb_id"],
                page=args.get("page", 1),
                page_size=args.get("page_size", 20),
            )
        elif name == "list_wiki_folders":
            result = client.list_wiki_folders(
                args["kb_id"], args.get("parent_id", "")
            )
        elif name == "create_wiki_folder":
            result = client.create_wiki_folder(
                args["kb_id"], args["name"], args.get("parent_id", "")
            )
        elif name == "update_wiki_folder":
            result = client.update_wiki_folder(
                args["kb_id"],
                args["folder_id"],
                name=args.get("name"),
                parent_id=args.get("parent_id"),
                move_parent=args.get("move_parent", False),
            )
        elif name == "delete_wiki_folder":
            result = client.delete_wiki_folder(args["kb_id"], args["folder_id"])
        elif name == "wiki_graph":
            result = client.wiki_graph(
                args["kb_id"],
                mode=args.get("mode", "overview"),
                center=args.get("center", ""),
                depth=args.get("depth"),
                limit=args.get("limit"),
                types=args.get("types", ""),
            )
        elif name == "wiki_stats":
            result = client.wiki_stats(args["kb_id"])
        elif name == "wiki_index_view":
            result = client.wiki_index_view(
                args["kb_id"],
                limit=args.get("limit", 50),
                types=args.get("types", ""),
            )
        elif name == "wiki_rebuild_links":
            result = client.wiki_rebuild_links(args["kb_id"])
        elif name == "wiki_lint":
            result = client.wiki_lint(args["kb_id"])
        elif name == "wiki_auto_fix":
            result = client.wiki_auto_fix(args["kb_id"])
        elif name == "wiki_log":
            result = client.wiki_log(
                args["kb_id"],
                limit=args.get("limit", 50),
                cursor=args.get("cursor", ""),
            )
        elif name == "wiki_list_issues":
            result = client.wiki_list_issues(
                args["kb_id"],
                slug=args.get("slug", ""),
                status=args.get("status", ""),
            )
        elif name == "wiki_update_issue_status":
            result = client.wiki_update_issue_status(
                args["kb_id"], args["issue_id"], args["status"]
            )
        elif name == "tokenize":
            result = client.tokenize(
                args["text"],
                mode=args.get("mode", "cut"),
                stopwords=args.get("stopwords"),
            )
        else:
            raise ValueError(f"Unknown tool: {name}")

        return [types.TextContent(type="text", text=str(result))]
    except Exception as exc:
        logger.exception("Tool call %s failed", name)
        return [types.TextContent(type="text", text=f"Error: {exc}")]


# ---------------------------------------------------------------------------
# Streamable HTTP session manager (single instance — KB scoping via tool args)
# ---------------------------------------------------------------------------

_session_manager = StreamableHTTPSessionManager(
    app=mcp_server,
    event_store=None,      # 不需要断线重放
    json_response=False,   # 响应走 SSE 流（单端点内），兼容 agno/mcp 客户端
    stateless=False,       # 有会话：initialize 时绑定租户 key 到 run task
)


# ---------------------------------------------------------------------------
# ASGI helpers
# ---------------------------------------------------------------------------


def _headers(scope: dict) -> dict[str, str]:
    return {
        k.decode("latin-1").lower(): v.decode("latin-1")
        for k, v in scope.get("headers", [])
    }


def _api_key_from(scope: dict) -> str:
    """Read the WeKnora API key from the X-API-Key header (header-only)."""
    return _headers(scope).get("x-api-key", "").strip()


async def _send_json(send: Any, status: int, body: bytes) -> None:
    await send(
        {
            "type": "http.response.start",
            "status": status,
            "headers": [[b"content-type", b"application/json"]],
        }
    )
    await send({"type": "http.response.body", "body": body})


async def _send_401(send: Any) -> None:
    await _send_json(send, 401, b'{"error":"unauthorized"}')


# ---------------------------------------------------------------------------
# ASGI endpoints
# ---------------------------------------------------------------------------


async def mcp_streamable(scope: dict, receive: Any, send: Any) -> None:
    """Handle ``/mcp`` — Streamable HTTP endpoint（POST/GET/DELETE）。

    单层鉴权：X-API-Key 租户 key（每个请求都带）。会话建立前先做前置验证
    （后端探针 + TTL 缓存），无效或权限不足的 key 直接 401，进不了
    initialize / tools/list，枚举面关闭。initialize 请求会创建会话的
    server-run task（task 创建时复制当前 context），contextvar 绑定的租户
    key 随之固定到该会话；后续请求经 memory stream 在同一 task 内执行，
    工具调用读到的即本会话 key，且每次工具调用仍由后端实时强验。
    """
    api_key = _api_key_from(scope)
    if not api_key:
        await _send_json(send, 401, b'{"error":"missing X-API-Key header"}')
        return

    # 前置验证：无效 key 不允许建立会话
    if not await _key_valid(api_key):
        await _send_401(send)
        return

    token = _api_key_context.set(api_key)
    try:
        await _session_manager.handle_request(scope, receive, send)
    finally:
        _api_key_context.reset(token)


# ---------------------------------------------------------------------------
# Health check
# ---------------------------------------------------------------------------


async def health_check(request: Any) -> JSONResponse:
    """``GET /health`` — simple liveness probe."""
    return JSONResponse({"status": "ok"})


# ---------------------------------------------------------------------------
# Starlette application
# ---------------------------------------------------------------------------


@asynccontextmanager
async def _lifespan(_app: Starlette) -> AsyncIterator[None]:
    """Application lifespan: run the session manager task group."""
    async with _session_manager.run():
        yield


def create_app() -> Starlette:
    """Build and return the Starlette application."""
    routes = [
        Route("/health", endpoint=health_check),
        Mount("/mcp", app=mcp_streamable),
    ]
    return Starlette(routes=routes, lifespan=_lifespan)


# ---------------------------------------------------------------------------
# Entrypoint
# ---------------------------------------------------------------------------


def main() -> None:
    import argparse

    parser = argparse.ArgumentParser(description="WeKnora MCP Gateway")
    parser.add_argument(
        "--host",
        default=os.getenv("MCP_HOST", "0.0.0.0"),
        help="Bind address (default: 0.0.0.0)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.getenv("MCP_PORT", "8000")),
        help="Listen port (default: 8000)",
    )
    args = parser.parse_args()

    app = create_app()
    uvicorn.run(app, host=args.host, port=args.port, log_level="info")


if __name__ == "__main__":
    main()
