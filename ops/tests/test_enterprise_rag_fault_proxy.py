from __future__ import annotations

import importlib.util
import io
import json
import sys
import threading
import unittest
import urllib.error
import urllib.request
from contextlib import redirect_stderr
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


MODULE_PATH = Path(__file__).parents[1] / "enterprise-rag-fault-proxy.py"
SPEC = importlib.util.spec_from_file_location("enterprise_rag_fault_proxy", MODULE_PATH)
assert SPEC and SPEC.loader
PROXY = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PROXY
SPEC.loader.exec_module(PROXY)


class UpstreamHandler(BaseHTTPRequestHandler):
    calls: list[tuple[str, str, bytes]] = []

    def log_message(self, format: str, *args: object) -> None:
        return

    def do_GET(self) -> None:
        self._reply()

    def do_POST(self) -> None:
        self._reply()

    def _reply(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        self.__class__.calls.append((self.command, self.path, body))
        response = json.dumps(
            {"method": self.command, "path": self.path, "size": len(body)}
        ).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)


class FaultProxyTest(unittest.TestCase):
    def setUp(self) -> None:
        UpstreamHandler.calls = []
        self.upstream = ThreadingHTTPServer(("127.0.0.1", 0), UpstreamHandler)
        self.upstream_thread = threading.Thread(
            target=self.upstream.serve_forever, daemon=True
        )
        self.upstream_thread.start()
        config = PROXY.ProxyConfig(
            listen_port=0,
            upstream_host="127.0.0.1",
            upstream_port=self.upstream.server_port,
            fail_path="/v1/rerank",
            match_text=b"enterprise-rag-fault-marker",
            fault_id="fault-test-0001",
            timeout_seconds=2,
        )
        self.proxy = PROXY.FaultProxyServer(config)
        self.proxy_thread = threading.Thread(target=self.proxy.serve_forever, daemon=True)
        self.proxy_thread.start()
        self.base_url = f"http://127.0.0.1:{self.proxy.server_port}"

    def tearDown(self) -> None:
        self.proxy.shutdown()
        self.proxy.server_close()
        self.upstream.shutdown()
        self.upstream.server_close()
        self.proxy_thread.join(timeout=2)
        self.upstream_thread.join(timeout=2)

    def request(self, path: str, body: bytes | None = None) -> tuple[int, dict]:
        request = urllib.request.Request(
            self.base_url + path,
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST" if body is not None else "GET",
        )
        try:
            response = urllib.request.urlopen(request, timeout=2)
            return response.status, json.loads(response.read())
        except urllib.error.HTTPError as error:
            return error.code, json.loads(error.read())

    def test_rejects_only_exact_marked_path(self) -> None:
        status, payload = self.request(
            "/v1/rerank", b'{"query":"enterprise-rag-fault-marker"}'
        )
        self.assertEqual(status, 503)
        self.assertEqual(payload["error"]["code"], "FAULT_INJECTED")
        self.assertEqual(UpstreamHandler.calls, [])

        status, payload = self.request(
            "/v1/embeddings", b'{"texts":["enterprise-rag-fault-marker"]}'
        )
        self.assertEqual(status, 200)
        self.assertEqual(payload["path"], "/v1/embeddings")
        self.assertEqual(len(UpstreamHandler.calls), 1)

        status, _ = self.request("/v1/rerank", b'{"query":"ordinary request"}')
        self.assertEqual(status, 200)
        self.assertEqual(len(UpstreamHandler.calls), 2)

    def test_status_exposes_counts_but_not_marker(self) -> None:
        self.request("/v1/rerank", b'{"query":"enterprise-rag-fault-marker"}')
        self.request("/v1/routes", b'{"content":"enterprise-rag-fault-marker"}')
        status, payload = self.request("/__fault/status")
        self.assertEqual(status, 200)
        self.assertEqual(payload["rejected_requests"], 1)
        self.assertEqual(payload["forwarded_requests"], 1)
        self.assertNotIn("enterprise-rag-fault-marker", json.dumps(payload))
        self.assertEqual(len(payload["match_sha256"]), 64)

    def test_health_is_served_locally(self) -> None:
        status, payload = self.request("/__fault/healthz")
        self.assertEqual(status, 200)
        self.assertEqual(payload, {"status": "ok"})
        self.assertEqual(UpstreamHandler.calls, [])

    def test_parse_config_requires_loopback_upstream(self) -> None:
        with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            PROXY.parse_config([
                "--listen-port",
                "18084",
                "--upstream",
                "http://172.31.50.2:18083",
                "--fail-path",
                "/v1/rerank",
                "--match-text",
                "enterprise-rag-fault-marker",
                "--fault-id",
                "fault-test-0001",
            ])


if __name__ == "__main__":
    unittest.main()
