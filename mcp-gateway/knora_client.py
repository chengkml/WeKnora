"""
WeKnora Gateway — REST API Client

A stateless client that calls the WeKnora Go backend over HTTP.
Every request goes directly to WEKNORA_BASE_URL; no caching.
"""

from __future__ import annotations

import logging
import os
from typing import Any, Dict, Optional

import requests
from requests.exceptions import RequestException

logger = logging.getLogger(__name__)


class WeKnoraGatewayClient:
    """Stateless HTTP client for the WeKnora Go REST API.

    All methods are read-only.  The caller is responsible for providing
    *kb_id* (the knowledge-base UUID) — this client never resolves names.
    """

    def __init__(self, base_url: str, api_key: str):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key

        self.verify_ssl = os.getenv("WEKNORA_VERIFY_SSL", "true").lower() != "false"
        if not self.verify_ssl:
            logger.warning(
                "SSL verification DISABLED (WEKNORA_VERIFY_SSL=false). "
                "Not recommended for production."
            )

        self.session = requests.Session()
        self.session.verify = self.verify_ssl
        self.session.headers.update(
            {
                "X-API-Key": api_key,
                "Content-Type": "application/json",
            }
        )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _request(
        self, method: str, path: str, **kwargs: Any
    ) -> Dict[str, Any]:
        url = f"{self.base_url}{path}"
        try:
            resp = self.session.request(method, url, **kwargs)
            resp.raise_for_status()
            return resp.json()
        except RequestException as exc:
            logger.error("API request failed: %s %s — %s", method, path, exc)
            raise

    # ------------------------------------------------------------------
    # Knowledge-base discovery
    # ------------------------------------------------------------------

    def list_knowledge_bases(self) -> Dict[str, Any]:
        """Return all knowledge bases visible to the API key."""
        return self._request("GET", "/knowledge-bases")

    def get_knowledge_base(self, kb_id: str) -> Dict[str, Any]:
        """Return metadata for a single knowledge base."""
        return self._request("GET", f"/knowledge-bases/{kb_id}")

    # ------------------------------------------------------------------
    # Search & document listing (read-only)
    # ------------------------------------------------------------------

    def hybrid_search(
        self, kb_id: str, query: str, config: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        """Semantic + keyword hybrid search scoped to *kb_id*."""
        body: Dict[str, Any] = {"query_text": query}
        if config:
            body.update(config)
        return self._request(
            "POST", f"/knowledge-bases/{kb_id}/hybrid-search", json=body
        )

    def list_knowledge(
        self, kb_id: str, page: int = 1, page_size: int = 20
    ) -> Dict[str, Any]:
        """Paginated list of documents in a knowledge base."""
        return self._request(
            "GET",
            f"/knowledge-bases/{kb_id}/knowledge",
            params={"page": page, "page_size": page_size},
        )

    def get_knowledge(self, knowledge_id: str) -> Dict[str, Any]:
        """Metadata for a single document."""
        return self._request("GET", f"/knowledge/{knowledge_id}")

    def list_chunks(
        self, knowledge_id: str, page: int = 1, page_size: int = 20
    ) -> Dict[str, Any]:
        """Paginated list of chunks belonging to a document."""
        return self._request(
            "GET",
            f"/chunks/{knowledge_id}",
            params={"page": page, "page_size": page_size},
        )

    # ------------------------------------------------------------------
    # Wiki (read-only)
    # ------------------------------------------------------------------

    def wiki_search(self, kb_id: str, query: str, limit: int = 10) -> Dict[str, Any]:
        """Full-text wiki search within a knowledge base."""
        return self._request(
            "GET",
            f"/knowledgebase/{kb_id}/wiki/search",
            params={"q": query, "limit": limit},
        )

    def wiki_read_page(self, kb_id: str, slug: str) -> Dict[str, Any]:
        """Read a wiki page (markdown + metadata) by slug."""
        return self._request(
            "GET", f"/knowledgebase/{kb_id}/wiki/pages/{slug}"
        )
