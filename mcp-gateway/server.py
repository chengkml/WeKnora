#!/usr/bin/env python3
"""
WeKnora MCP Gateway — multi-tenant SSE MCP server.

Single Starlette process that exposes KB-scoped MCP endpoints:

    /mcp/<kb_id>/sse       → SSE session for a single knowledge base
    /mcp/__all__/sse        → SSE session for all accessible knowledge bases

Each path segment ``kb_id`` is bound to the SSE session so that every
tool call automatically scopes its queries to that knowledge base.
"""

from __future__ import annotations

import contextvars
import logging
import os
import secrets
import sys
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Any

import mcp.types as types
import uvicorn
from mcp.server import NotificationOptions, Server
from mcp.server.models import InitializationOptions
from mcp.server.sse import SseServerTransport
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
WEKNORA_API_KEY = os.getenv("WEKNORA_API_KEY", "")

# Gateway-level auth token.  When set, *every* request must carry
# ``Authorization: Bearer <token>``.
MCP_GATEWAY_AUTH_TOKEN = os.getenv("MCP_GATEWAY_AUTH_TOKEN", "").strip()


def require_gateway_auth() -> str:
    """Exit if the gateway auth token is missing when needed (always required)."""
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
# Per-connection KB-ID via contextvars
# ---------------------------------------------------------------------------

_kb_context: contextvars.ContextVar[str] = contextvars.ContextVar("kb_id")


def current_kb_id() -> str:
    """Return the knowledge-base UUID bound to the current SSE session."""
    return _kb_context.get()


# ---------------------------------------------------------------------------
# Global client (stateless — safe to share)
# ---------------------------------------------------------------------------

_gateway_client = WeKnoraGatewayClient(WEKNORA_BASE_URL, WEKNORA_API_KEY)

# ---------------------------------------------------------------------------
# MCP Server
# ---------------------------------------------------------------------------

mcp_server = Server("weknora-mcp-gateway")


@mcp_server.list_tools()
async def handle_list_tools() -> list[types.Tool]:
    """Expose only read‑only tools, each scoped to the session's KB."""
    return [
        types.Tool(
            name="hybrid_search",
            description="Hybrid (semantic + keyword) search within the knowledge base. "
            "Results include document text, metadata, and relevance scores.",
            inputSchema={
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "Natural‑language search query",
                    },
                    "vector_threshold": {
                        "type": "number",
                        "description": "Vector similarity threshold (0–1)",
                        "default": 0.5,
                    },
                    "keyword_threshold": {
                        "type": "number",
                        "description": "Keyword match threshold (0–1)",
                        "default": 0.3,
                    },
                    "match_count": {
                        "type": "integer",
                        "description": "Max results to return",
                        "default": 5,
                    },
                },
                "required": ["query"],
            },
        ),
        types.Tool(
            name="list_documents",
            description="List all documents in the knowledge base with pagination.",
            inputSchema={
                "type": "object",
                "properties": {
                    "page": {
                        "type": "integer",
                        "description": "Page number (1‑based)",
                        "default": 1,
                    },
                    "page_size": {
                        "type": "integer",
                        "description": "Items per page",
                        "default": 20,
                    },
                },
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
                        "description": "Page number (1‑based)",
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
            description="Search wiki pages within the knowledge base.",
            inputSchema={
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "Full‑text search query",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Max results",
                        "default": 10,
                    },
                },
                "required": ["query"],
            },
        ),
        types.Tool(
            name="wiki_read_page",
            description="Read a wiki page by its slug. Returns markdown content, "
            "metadata, and linked pages.",
            inputSchema={
                "type": "object",
                "properties": {
                    "slug": {
                        "type": "string",
                        "description": "Wiki page slug (URL path)",
                    }
                },
                "required": ["slug"],
            },
        ),
        types.Tool(
            name="list_knowledge_bases",
            description="List knowledge base(s) accessible in this session. "
            "For a single‑KB endpoint this returns only that KB; "
            "for /mcp/__all__/sse it returns every KB the API key can access.",
            inputSchema={
                "type": "object",
                "properties": {},
            },
        ),
    ]


@mcp_server.call_tool()
async def handle_call_tool(
    name: str, arguments: dict[str, Any] | None
) -> list[types.TextContent]:
    """Dispatch tool calls, injecting the session's kb_id."""
    kb_id = current_kb_id()
    args = arguments or {}

    try:
        if name == "hybrid_search":
            config = {
                k: args[k]
                for k in ("vector_threshold", "keyword_threshold", "match_count")
                if k in args
            }
            result = _gateway_client.hybrid_search(
                kb_id, args["query"], config or None
            )
        elif name == "list_documents":
            result = _gateway_client.list_knowledge(
                kb_id,
                page=args.get("page", 1),
                page_size=args.get("page_size", 20),
            )
        elif name == "get_document":
            result = _gateway_client.get_knowledge(args["knowledge_id"])
        elif name == "list_chunks":
            result = _gateway_client.list_chunks(
                args["knowledge_id"],
                page=args.get("page", 1),
                page_size=args.get("page_size", 20),
            )
        elif name == "wiki_search":
            result = _gateway_client.wiki_search(
                kb_id,
                query=args["query"],
                limit=args.get("limit", 10),
            )
        elif name == "wiki_read_page":
            result = _gateway_client.wiki_read_page(kb_id, args["slug"])
        elif name == "list_knowledge_bases":
            if kb_id == "__all__":
                result = _gateway_client.list_knowledge_bases()
            else:
                result = _gateway_client.get_knowledge_base(kb_id)
        else:
            raise ValueError(f"Unknown tool: {name}")

        return [types.TextContent(type="text", text=str(result))]
    except Exception as exc:
        logger.exception("Tool call %s failed", name)
        return [types.TextContent(type="text", text=f"Error: {exc}")]


async def mcp_initialization_options() -> InitializationOptions:
    """Return MCP initialization metadata."""
    return InitializationOptions(
        server_name="weknora-mcp-gateway",
        server_version="0.1.0",
        capabilities=mcp_server.get_capabilities(
            notification_options=NotificationOptions(),
            experimental_capabilities={},
        ),
    )


# ---------------------------------------------------------------------------
# SSE Transport cache (one per kb_id)
# ---------------------------------------------------------------------------

_transport_cache: dict[str, SseServerTransport] = {}


def _get_transport(kb_id: str) -> SseServerTransport:
    if kb_id not in _transport_cache:
        _transport_cache[kb_id] = SseServerTransport(
            f"/{kb_id}/messages"
        )
    return _transport_cache[kb_id]


# ---------------------------------------------------------------------------
# ASGI endpoints
# ---------------------------------------------------------------------------

# Special sentinel for the "all KBs" route
ALL_KB_SENTINEL = "__all__"


async def _auth_ok(scope: dict) -> bool:
    """Check Bearer token against the gateway auth token (if configured)."""
    if not MCP_GATEWAY_AUTH_TOKEN:
        return True
    headers = {
        k.decode("latin-1").lower(): v.decode("latin-1")
        for k, v in scope.get("headers", [])
    }
    auth = headers.get("authorization", "")
    if auth.lower().startswith("bearer "):
        provided = auth[7:].strip()
        return bool(provided) and secrets.compare_digest(
            provided, MCP_GATEWAY_AUTH_TOKEN
        )
    return False


async def _send_401(send: Any) -> None:
    body = b'{"error":"unauthorized"}'
    await send(
        {
            "type": "http.response.start",
            "status": 401,
            "headers": [[b"content-type", b"application/json"]],
        }
    )
    await send({"type": "http.response.body", "body": body})


async def _send_404(send: Any) -> None:
    body = b"Not Found"
    await send(
        {
            "type": "http.response.start",
            "status": 404,
            "headers": [[b"content-type", b"text/plain"]],
        }
    )
    await send({"type": "http.response.body", "body": body})


async def _send_405(send: Any) -> None:
    body = b"Method Not Allowed"
    await send(
        {
            "type": "http.response.start",
            "status": 405,
            "headers": [[b"content-type", b"text/plain"]],
        }
    )
    await send({"type": "http.response.body", "body": body})


async def _send_200_head(send: Any) -> None:
    """Respond to HEAD with SSE content-type but no body.

    Clients (e.g. hermes) send HEAD to probe whether the SSE endpoint is
    alive.  Returning 200 with the SSE content-type signals readiness
    without establishing an actual SSE session.
    """
    headers_list = [
        [b"content-type", b"text/event-stream; charset=utf-8"],
        [b"cache-control", b"no-store"],
        [b"connection", b"keep-alive"],
    ]
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": headers_list,
        }
    )
    await send({"type": "http.response.body", "body": b""})


async def mcp_sse(scope: dict, receive: Any, send: Any) -> None:
    """Handle ``GET /mcp/{kb_id}/sse`` — establish an SSE session.

    ``Mount("/mcp")`` updates ``root_path`` but does **not** strip
    ``scope["path"]``, so the path seen here is still
    ``/mcp/{kb_id}/sse`` → parts = ["mcp", "{kb_id}", "sse"].

    ``HEAD`` requests are answered with 200 + SSE content-type (no body)
    so that probing clients (hermes, etc.) can verify the endpoint is
    alive without starting a full SSE session.
    """
    if scope["method"] == "HEAD":
        await _send_200_head(send)
        return

    if scope["method"] != "GET":
        await _send_405(send)
        return

    if not await _auth_ok(scope):
        await _send_401(send)
        return

    # Mount("/mcp") updates root_path but does NOT strip scope["path"],
    # so the path seen here is still /mcp/{kb_id}/sse.
    # parts = ["mcp", "{kb_id}", "sse"]
    path = scope["path"]
    parts = path.strip("/").split("/")
    if len(parts) < 3 or parts[-1] != "sse":
        await _send_404(send)
        return

    kb_id = parts[1]
    transport = _get_transport(kb_id)

    token = _kb_context.set(kb_id)
    try:
        async with transport.connect_sse(scope, receive, send) as streams:
            init_opts = await mcp_initialization_options()
            await mcp_server.run(
                streams[0], streams[1], init_opts
            )
    finally:
        _kb_context.reset(token)


async def mcp_messages(scope: dict, receive: Any, send: Any) -> None:
    """Handle ``POST /mcp/{kb_id}/messages`` — inbound MCP messages.

    After Mount strips ``/mcp``, ``scope["path"]`` is ``/{kb_id}/messages``.
    """
    if scope["method"] not in ("POST", "OPTIONS"):
        await _send_405(send)
        return

    if not await _auth_ok(scope):
        await _send_401(send)
        return

    # Mount does NOT strip scope["path"], so path is still /mcp/{kb_id}/messages
    # parts = ["mcp", "{kb_id}", "messages"]
    path = scope["path"]
    parts = path.strip("/").split("/")
    if len(parts) < 3:
        await _send_404(send)
        return

    kb_id = parts[1]
    transport = _get_transport(kb_id)
    await transport.handle_post_message(scope, receive, send)


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
    """Application lifespan: validate config on startup."""
    require_gateway_auth()
    yield


def create_app() -> Starlette:
    """Build and return the Starlette application."""
    routes = [
        Route("/health", endpoint=health_check),
        # SSE and message endpoints for KB-scoped access
        Mount("/mcp", app=mcp_router),
    ]
    return Starlette(routes=routes, lifespan=_lifespan)


async def mcp_router(scope: dict, receive: Any, send: Any) -> None:
    """Dispatch incoming MCP requests to the SSE or messages handler.

    Mounted at ``/mcp``; ``scope["path"]`` is the original path
    ``/mcp/{kb_id}/sse`` or ``/mcp/{kb_id}/messages``
    since Mount does not strip it.
    """
    path: str = scope["path"]
    segments = path.strip("/").split("/")  # ["mcp", "{kb_id}", "sse"|"messages"]

    if len(segments) < 2:
        await _send_404(send)
        return

    action = segments[-1]  # "sse" or "messages"
    if action == "sse":
        await mcp_sse(scope, receive, send)
    elif action == "messages":
        await mcp_messages(scope, receive, send)
    else:
        await _send_404(send)


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
