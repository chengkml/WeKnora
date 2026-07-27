#!/usr/bin/env python3
"""
WeKnora MCP Gateway — multi-tenant Streamable HTTP MCP server (per-user API key edition).

Single Starlette process exposing ONE MCP endpoint:

    /mcp            → Streamable HTTP（POST/GET/DELETE 单端点双向）

Two-layer auth (both via headers, no query params):

    Authorization: Bearer <MCP_GATEWAY_AUTH_TOKEN>   — gateway-level shared secret
    X-API-Key: <weknora tenant api key>              — selects the WeKnora tenant

The X-API-Key is bound to the MCP session via contextvars: the session's
server-run task is created on the initialize request and inherits the
context (task creation copies contextvars), so every tool call in that
session resolves (or creates) a WeKnoraGatewayClient for that key — one
gateway process serves every tenant. Knowledge-base scoping is explicit:
KB tools take a required ``kb_id`` argument (call ``list_knowledge_bases`` first).
"""

from __future__ import annotations

import contextvars
import logging
import os
import secrets
import sys
import threading
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

from knora_client import WeKnoraGatewayClient

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("mcp-gateway")

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

WEKNORA_BASE_URL = os.getenv("WEKNORA_BASE_URL", "http://localhost:8080/api/v1")

# Gateway-level auth token.  When set, *every* request must carry
# ``Authorization: Bearer <token>``.
MCP_GATEWAY_AUTH_TOKEN = os.getenv("MCP_GATEWAY_AUTH_TOKEN", "").strip()


def require_gateway_auth() -> str:
    """Exit if the gateway auth token is missing (always required)."""
    if not MCP_GATEWAY_AUTH_TOKEN:
        logger.error(
            "MCP_GATEWAY_AUTH_TOKEN is required. "
            "Set a strong shared secret; clients must send "
            "Authorization: Bearer <token>."
        )
        sys.exit(1)
    logger.info(
        "MCP_GATEWAY_AUTH_TOKEN is configured; all requests will be authenticated."
    )
    return MCP_GATEWAY_AUTH_TOKEN


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


async def _bearer_ok(scope: dict) -> bool:
    """Check Bearer token against the gateway auth token."""
    if not MCP_GATEWAY_AUTH_TOKEN:
        return True
    auth = _headers(scope).get("authorization", "")
    if auth.lower().startswith("bearer "):
        provided = auth[7:].strip()
        return bool(provided) and secrets.compare_digest(
            provided, MCP_GATEWAY_AUTH_TOKEN
        )
    return False


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

    双层鉴权：Bearer 网关令牌 + X-API-Key 租户 key（每个请求都带）。
    initialize 请求会创建会话的 server-run task（task 创建时复制当前
    context），contextvar 绑定的租户 key 随之固定到该会话；后续请求经
    memory stream 在同一 task 内执行，工具调用读到的即本会话 key。
    """
    if not await _bearer_ok(scope):
        await _send_401(send)
        return

    api_key = _api_key_from(scope)
    if not api_key:
        await _send_json(send, 401, b'{"error":"missing X-API-Key header"}')
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
    """Application lifespan: validate config + run session manager task group."""
    require_gateway_auth()
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
