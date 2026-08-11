"""Focused tests for the WEK-10 X-API-Key single-layer auth merge.

Verifies the gateway's session-establishment gate without a live backend:
the ``mcp_streamable`` ASGI endpoint is exercised with a mocked backend probe
(200/401/403/5xx), and the removal of the Bearer layer is asserted by
inspecting the module.
"""

from __future__ import annotations

import asyncio
import hashlib
import unittest
from unittest import mock

import server
from knora_client import probe_api_key


def _asgi_scope(headers: list[tuple[str, str]]):
    return {
        "type": "http",
        "method": "POST",
        "path": "/mcp",
        "headers": [
            (k.lower().encode("latin-1"), v.encode("latin-1"))
            for k, v in headers
        ],
    }


class _SendRecorder:
    def __init__(self):
        self.messages = []

    async def __call__(self, message):
        self.messages.append(message)


class _ReceiveStub:
    async def __call__(self):
        # No more body — the session manager would need real MCP framing,
        # which is out of scope here; we only assert the auth gate response.
        return {"type": "http.disconnect"}


_LOOP = asyncio.new_event_loop()


def run(coro):
    return _LOOP.run_until_complete(coro)


class AuthMergeTests(unittest.TestCase):
    def setUp(self):
        server._valid_cache.clear()

    def test_bearer_layer_removed(self):
        """Bearer auth machinery must be gone."""
        self.assertFalse(hasattr(server, "MCP_GATEWAY_AUTH_TOKEN"))
        self.assertFalse(hasattr(server, "_bearer_ok"))
        self.assertFalse(hasattr(server, "require_gateway_auth"))
        self.assertNotIn("secrets", server.__dict__)

    def test_missing_api_key_401(self):
        send = _SendRecorder()
        run(server.mcp_streamable(_asgi_scope([]), _ReceiveStub(), send))
        self.assertEqual(send.messages[0]["status"], 401)
        self.assertIn(
            b"missing X-API-Key header", send.messages[1]["body"]
        )

    def test_invalid_key_rejected_before_session(self):
        with mock.patch("server._key_valid", new=mock.AsyncMock(return_value=False)):
            send = _SendRecorder()
            run(server.mcp_streamable(
                _asgi_scope([("x-api-key", "deadbeef")]), _ReceiveStub(), send
            ))
        self.assertEqual(send.messages[0]["status"], 401)
        self.assertIn(b"unauthorized", send.messages[1]["body"])

    def test_valid_key_passes_to_session_manager(self):
        with mock.patch("server._key_valid", new=mock.AsyncMock(return_value=True)):
            with mock.patch(
                "server._session_manager.handle_request",
                new=mock.AsyncMock(),
            ) as handle:
                send = _SendRecorder()
                run(server.mcp_streamable(
                    _asgi_scope([("x-api-key", "good-key")]), _ReceiveStub(), send
                ))
                handle.assert_awaited_once()
        self.assertEqual(send.messages, [])  # manager handled the response

    def test_key_valid_cache_hit(self):
        """A cached 'valid' result short-circuits the probe."""
        api_key = "cached-key"
        key_id = hashlib.sha256(api_key.encode()).hexdigest()
        server._valid_cache[key_id] = (True, __import__("time").time() + 300)
        with mock.patch(
            "server._probe_key", side_effect=AssertionError("should not probe")
        ):
            self.assertTrue(run(server._key_valid(api_key)))

    def test_key_valid_cache_miss_probes(self):
        with mock.patch("server._probe_key", return_value=True) as probe:
            self.assertTrue(run(server._key_valid("probe-me")))
        probe.assert_called_once_with("probe-me")
        # Second call hits cache.
        with mock.patch(
            "server._probe_key", side_effect=AssertionError("should not probe")
        ):
            self.assertTrue(run(server._key_valid("probe-me")))

    def test_key_valid_false_on_reject(self):
        with mock.patch("server._probe_key", return_value=False):
            self.assertFalse(run(server._key_valid("bad-key")))

    def test_probe_api_key_200(self):
        with mock.patch(
            "knora_client.requests.get"
        ) as get:
            get.return_value = mock.Mock(status_code=200)
            self.assertTrue(probe_api_key("http://b", "k"))
            _, kwargs = get.call_args
            self.assertEqual(kwargs["headers"]["X-API-Key"], "k")

    def test_probe_api_key_401_and_403(self):
        for code in (401, 403):
            with mock.patch("knora_client.requests.get") as get:
                get.return_value = mock.Mock(status_code=code)
                self.assertFalse(probe_api_key("http://b", "k"))

    def test_probe_api_key_5xx_and_network_error(self):
        with mock.patch("knora_client.requests.get") as get:
            get.return_value = mock.Mock(status_code=500)
            self.assertFalse(probe_api_key("http://b", "k"))
        with mock.patch(
            "knora_client.requests.get",
            side_effect=__import__("requests").exceptions.ConnectionError,
        ):
            self.assertFalse(probe_api_key("http://b", "k"))


if __name__ == "__main__":
    unittest.main()
