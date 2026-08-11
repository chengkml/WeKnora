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

    Callers provide *kb_id* (the knowledge-base UUID) explicitly — this
    client never resolves names.  Read-only discovery/search live alongside
    full Wiki write methods (page/folder CRUD, graph, link maintenance).
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
            if resp.status_code in (204, 205) or not resp.content:
                # DELETE + some writes return an empty body (204/205); the raw
                # methods return *other data* via headers/bools, so surface a
                # stable sentinel instead of crashing on resp.json().
                return {"success": True}
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

    # ------------------------------------------------------------------
    # Wiki page CRUD
    # ------------------------------------------------------------------

    def create_wiki_page(self, kb_id: str, page: Dict[str, Any]) -> Dict[str, Any]:
        """Create a wiki page in the knowledge base."""
        return self._request(
            "POST", f"/knowledgebase/{kb_id}/wiki/pages", json=page
        )

    def update_wiki_page(
        self, kb_id: str, slug: str, page: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Update a wiki page (identified by slug) with the given fields."""
        return self._request(
            "PUT", f"/knowledgebase/{kb_id}/wiki/pages/{slug}", json=page
        )

    def delete_wiki_page(self, kb_id: str, slug: str) -> Dict[str, Any]:
        """Soft-delete a wiki page by slug (204 → {"success": True})."""
        return self._request(
            "DELETE", f"/knowledgebase/{kb_id}/wiki/pages/{slug}"
        )

    def move_wiki_page(
        self, kb_id: str, slug: str, folder_id: str = ""
    ) -> Dict[str, Any]:
        """Relocate a page to a folder (slug carried in body, not path)."""
        return self._request(
            "PUT",
            f"/knowledgebase/{kb_id}/wiki/move-page",
            json={"slug": slug, "folder_id": folder_id},
        )

    def list_wiki_pages(
        self, kb_id: str, page: int = 1, page_size: int = 20
    ) -> Dict[str, Any]:
        """Paginated list of wiki pages in a knowledge base."""
        return self._request(
            "GET",
            f"/knowledgebase/{kb_id}/wiki/pages",
            params={"page": page, "page_size": page_size},
        )

    # ------------------------------------------------------------------
    # Wiki folder CRUD
    # ------------------------------------------------------------------

    def list_wiki_folders(self, kb_id: str, parent_id: str = "") -> Dict[str, Any]:
        """List direct child folders of a parent (empty parent_id = root)."""
        params: Dict[str, Any] = {}
        if parent_id:
            params["parent_id"] = parent_id
        return self._request(
            "GET", f"/knowledgebase/{kb_id}/wiki/folders", params=params
        )

    def create_wiki_folder(
        self, kb_id: str, name: str, parent_id: str = ""
    ) -> Dict[str, Any]:
        """Create a new (initially empty) wiki folder."""
        return self._request(
            "POST",
            f"/knowledgebase/{kb_id}/wiki/folders",
            json={"name": name, "parent_id": parent_id},
        )

    def update_wiki_folder(
        self,
        kb_id: str,
        folder_id: str,
        name: Optional[str] = None,
        parent_id: Optional[str] = None,
        move_parent: bool = False,
    ) -> Dict[str, Any]:
        """Rename and/or reparent a wiki folder."""
        body: Dict[str, Any] = {}
        if name is not None:
            body["name"] = name
        if parent_id is not None:
            body["parent_id"] = parent_id
        if move_parent:
            body["move_parent"] = True
        return self._request(
            "PUT", f"/knowledgebase/{kb_id}/wiki/folders/{folder_id}", json=body
        )

    def delete_wiki_folder(self, kb_id: str, folder_id: str) -> Dict[str, Any]:
        """Delete an empty wiki folder (204 on success)."""
        return self._request(
            "DELETE", f"/knowledgebase/{kb_id}/wiki/folders/{folder_id}"
        )

    # ------------------------------------------------------------------
    # Wiki graph / stats / index / log
    # ------------------------------------------------------------------

    def wiki_graph(
        self,
        kb_id: str,
        mode: str = "overview",
        center: str = "",
        depth: Optional[int] = None,
        limit: Optional[int] = None,
        types: str = "",
    ) -> Dict[str, Any]:
        """Return a slice of the wiki link graph for visualization.

        mode=overview (default) returns the most-connected pages; mode=ego
        returns the BFS neighborhood of *center*. depth/limit are optional
        positive ints with backend defaults; types is a comma-separated
        page_type allow-list.
        """
        params: Dict[str, Any] = {"mode": mode}
        if center:
            params["center"] = center
        if depth is not None:
            params["depth"] = depth
        if limit is not None:
            params["limit"] = limit
        if types:
            params["types"] = types
        return self._request(
            "GET", f"/knowledgebase/{kb_id}/wiki/graph", params=params
        )

    def wiki_stats(self, kb_id: str) -> Dict[str, Any]:
        """Return aggregate statistics about the wiki."""
        return self._request("GET", f"/knowledgebase/{kb_id}/wiki/stats")

    def wiki_index_view(
        self, kb_id: str, limit: int = 50, types: str = ""
    ) -> Dict[str, Any]:
        """Get the wiki index view (cursor-paginated directory listing)."""
        params: Dict[str, Any] = {"limit": limit}
        if types:
            params["types"] = types
        return self._request(
            "GET", f"/knowledgebase/{kb_id}/wiki/index", params=params
        )

    def wiki_log(
        self, kb_id: str, limit: int = 50, cursor: str = ""
    ) -> Dict[str, Any]:
        """Get a paginated feed of wiki operation events (newest-first)."""
        params: Dict[str, Any] = {"limit": limit}
        if cursor:
            params["cursor"] = cursor
        return self._request(
            "GET", f"/knowledgebase/{kb_id}/wiki/log", params=params
        )

    # ------------------------------------------------------------------
    # Wiki link maintenance
    # ------------------------------------------------------------------

    def wiki_rebuild_links(self, kb_id: str) -> Dict[str, Any]:
        """Re-parse all pages and rebuild bidirectional link references."""
        return self._request(
            "POST", f"/knowledgebase/{kb_id}/wiki/rebuild-links"
        )

    def wiki_lint(self, kb_id: str) -> Dict[str, Any]:
        """Run a comprehensive health check over the wiki."""
        return self._request("GET", f"/knowledgebase/{kb_id}/wiki/lint")

    def wiki_auto_fix(self, kb_id: str) -> Dict[str, Any]:
        """Automatically fix fixable wiki issues (broken links, etc.)."""
        return self._request(
            "POST", f"/knowledgebase/{kb_id}/wiki/auto-fix"
        )

    # ------------------------------------------------------------------
    # Wiki issues
    # ------------------------------------------------------------------

    def wiki_list_issues(
        self, kb_id: str, slug: str = "", status: str = ""
    ) -> Dict[str, Any]:
        """List issues flagged on wiki pages (optionally filtered)."""
        params: Dict[str, Any] = {}
        if slug:
            params["slug"] = slug
        if status:
            params["status"] = status
        return self._request(
            "GET", f"/knowledgebase/{kb_id}/wiki/issues", params=params
        )

    def wiki_update_issue_status(
        self, kb_id: str, issue_id: str, status: str
    ) -> Dict[str, Any]:
        """Set an issue status (pending / ignored / resolved)."""
        return self._request(
            "PUT",
            f"/knowledgebase/{kb_id}/wiki/issues/{issue_id}/status",
            json={"status": status},
        )


def probe_api_key(base_url: str, api_key: str, timeout: float = 10.0) -> bool:
    """Validate an API key against the backend without raising.

    Probes ``GET {base_url}/knowledge-bases`` (the retrieve-capability
    endpoint the MCP gateway uses as its session-establishment gate):

      * 200 → the key is valid and may establish a session
      * 401 (invalid key) / 403 (valid but lacks retrieve capability) → rejected
      * any other status / network error → rejected (fail closed)

    Intended to be called from an async handler via ``asyncio.to_thread`` so
    the synchronous ``requests`` call does not block the event loop.
    """
    url = f"{base_url.rstrip('/')}/knowledge-bases"
    verify_ssl = os.getenv("WEKNORA_VERIFY_SSL", "true").lower() != "false"
    try:
        resp = requests.get(
            url,
            headers={"X-API-Key": api_key},
            timeout=timeout,
            verify=verify_ssl,
        )
    except RequestException as exc:
        logger.warning("API key probe failed (%s) — fail closed", exc)
        return False
    if resp.status_code == 200:
        return True
    if resp.status_code in (401, 403):
        return False
    logger.warning(
        "API key probe returned %s — fail closed", resp.status_code
    )
    return False
