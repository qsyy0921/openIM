#!/usr/bin/env python3
"""Loopback-only, marker-scoped HTTP fault proxy for Node2 RAG acceptance."""

from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import re
import threading
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


MAX_REQUEST_BYTES = 2 * 1024 * 1024
MAX_RESPONSE_BYTES = 4 * 1024 * 1024
HOP_BY_HOP_HEADERS = {
    "connection",
    "content-length",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "proxy-connection",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
}
FAULT_ID_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]{7,95}$")


@dataclass(frozen=True)
class ProxyConfig:
    listen_port: int
    upstream_host: str
    upstream_port: int
    fail_path: str
    match_text: bytes
    fault_id: str
    timeout_seconds: float = 30.0

    @property
    def match_digest(self) -> str:
        return hashlib.sha256(self.match_text).hexdigest()


@dataclass
class ProxyState:
    lock: threading.Lock = field(default_factory=threading.Lock)
    forwarded_requests: int = 0
    rejected_requests: int = 0
    upstream_failures: int = 0

    def increment(self, field_name: str) -> None:
        with self.lock:
            setattr(self, field_name, getattr(self, field_name) + 1)

    def snapshot(self) -> dict[str, int]:
        with self.lock:
            return {
                "forwarded_requests": self.forwarded_requests,
                "rejected_requests": self.rejected_requests,
                "upstream_failures": self.upstream_failures,
            }


class FaultProxyServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, config: ProxyConfig):
        super().__init__(("127.0.0.1", config.listen_port), FaultProxyHandler)
        self.config = config
        self.state = ProxyState()


class FaultProxyHandler(BaseHTTPRequestHandler):
    server: FaultProxyServer
    protocol_version = "HTTP/1.1"
    server_version = "OpenIMFaultProxy/1"
    sys_version = ""

    def log_message(self, format: str, *args: object) -> None:
        return

    def do_GET(self) -> None:
        if self.path == "/__fault/healthz":
            self._write_json(200, {"status": "ok"})
            return
        if self.path == "/__fault/status":
            payload: dict[str, object] = {
                "schema_version": 1,
                "fault_id": self.server.config.fault_id,
                "fail_path": self.server.config.fail_path,
                "match_sha256": self.server.config.match_digest,
            }
            payload.update(self.server.state.snapshot())
            self._write_json(200, payload)
            return
        self._handle_proxy()

    def do_POST(self) -> None:
        self._handle_proxy()

    def do_HEAD(self) -> None:
        self._handle_proxy()

    def do_PUT(self) -> None:
        self._write_json(405, {"error": {"code": "METHOD_NOT_ALLOWED"}})

    def do_DELETE(self) -> None:
        self._write_json(405, {"error": {"code": "METHOD_NOT_ALLOWED"}})

    def do_PATCH(self) -> None:
        self._write_json(405, {"error": {"code": "METHOD_NOT_ALLOWED"}})

    def _read_body(self) -> bytes | None:
        transfer_encoding = self.headers.get("Transfer-Encoding", "")
        if transfer_encoding:
            self._write_json(400, {"error": {"code": "CHUNKED_REQUEST_UNSUPPORTED"}})
            return None
        raw_length = self.headers.get("Content-Length", "0")
        try:
            length = int(raw_length)
        except ValueError:
            self._write_json(400, {"error": {"code": "INVALID_CONTENT_LENGTH"}})
            return None
        if length < 0 or length > MAX_REQUEST_BYTES:
            self._write_json(413, {"error": {"code": "REQUEST_TOO_LARGE"}})
            return None
        body = self.rfile.read(length)
        if len(body) != length:
            self._write_json(400, {"error": {"code": "INCOMPLETE_REQUEST"}})
            return None
        return body

    def _handle_proxy(self) -> None:
        body = self._read_body()
        if body is None:
            return
        request_path = urlsplit(self.path).path
        if (
            request_path == self.server.config.fail_path
            and self.server.config.match_text in body
        ):
            self.server.state.increment("rejected_requests")
            self._write_json(503, {"error": {"code": "FAULT_INJECTED"}})
            return

        headers = {
            name: value
            for name, value in self.headers.items()
            if name.lower() not in HOP_BY_HOP_HEADERS
        }
        headers["Host"] = (
            f"{self.server.config.upstream_host}:"
            f"{self.server.config.upstream_port}"
        )
        connection = http.client.HTTPConnection(
            self.server.config.upstream_host,
            self.server.config.upstream_port,
            timeout=self.server.config.timeout_seconds,
        )
        try:
            connection.request(self.command, self.path, body=body, headers=headers)
            response = connection.getresponse()
            response_body = response.read(MAX_RESPONSE_BYTES + 1)
            if len(response_body) > MAX_RESPONSE_BYTES:
                self.server.state.increment("upstream_failures")
                self._write_json(502, {"error": {"code": "UPSTREAM_RESPONSE_TOO_LARGE"}})
                return
            self.send_response(response.status)
            for name, value in response.getheaders():
                if name.lower() not in HOP_BY_HOP_HEADERS:
                    self.send_header(name, value)
            self.send_header("Content-Length", str(len(response_body)))
            self.send_header("Connection", "close")
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(response_body)
            self.close_connection = True
            self.server.state.increment("forwarded_requests")
        except (OSError, http.client.HTTPException):
            self.server.state.increment("upstream_failures")
            self._write_json(502, {"error": {"code": "UPSTREAM_UNAVAILABLE"}})
        finally:
            connection.close()

    def _write_json(self, status: int, value: dict[str, object]) -> None:
        body = json.dumps(value, separators=(",", ":"), sort_keys=True).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Connection", "close")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)
        self.close_connection = True


def parse_config(arguments: list[str] | None = None) -> ProxyConfig:
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen-port", required=True, type=int)
    parser.add_argument("--upstream", required=True)
    parser.add_argument("--fail-path", required=True)
    parser.add_argument("--match-text", required=True)
    parser.add_argument("--fault-id", required=True)
    parser.add_argument("--timeout-seconds", type=float, default=30.0)
    parsed = parser.parse_args(arguments)

    upstream = urlsplit(parsed.upstream)
    if (
        upstream.scheme != "http"
        or upstream.hostname not in {"127.0.0.1", "localhost", "::1"}
        or upstream.username is not None
        or upstream.password is not None
        or upstream.query
        or upstream.fragment
        or upstream.path not in {"", "/"}
    ):
        parser.error("upstream must be a credential-free loopback HTTP origin")
    try:
        upstream_port = upstream.port
    except ValueError:
        parser.error("upstream port is invalid")
    if upstream_port is None or not 1024 <= upstream_port <= 65535:
        parser.error("upstream port must be between 1024 and 65535")
    if not 1024 <= parsed.listen_port <= 65535:
        parser.error("listen port must be between 1024 and 65535")
    if parsed.listen_port == upstream_port:
        parser.error("listen and upstream ports must differ")
    if (
        not isinstance(parsed.fail_path, str)
        or not parsed.fail_path.startswith("/")
        or "?" in parsed.fail_path
        or "#" in parsed.fail_path
        or len(parsed.fail_path) > 128
    ):
        parser.error("fail path is invalid")
    if (
        not isinstance(parsed.match_text, str)
        or len(parsed.match_text.encode("utf-8")) < 8
        or len(parsed.match_text.encode("utf-8")) > 512
        or "\x00" in parsed.match_text
        or "\r" in parsed.match_text
        or "\n" in parsed.match_text
    ):
        parser.error("match text is invalid")
    if not FAULT_ID_PATTERN.fullmatch(parsed.fault_id):
        parser.error("fault ID is invalid")
    if not 0.1 <= parsed.timeout_seconds <= 120:
        parser.error("timeout must be between 0.1 and 120 seconds")

    return ProxyConfig(
        listen_port=parsed.listen_port,
        upstream_host=upstream.hostname,
        upstream_port=upstream_port,
        fail_path=parsed.fail_path,
        match_text=parsed.match_text.encode("utf-8"),
        fault_id=parsed.fault_id,
        timeout_seconds=parsed.timeout_seconds,
    )


def main() -> int:
    config = parse_config()
    server = FaultProxyServer(config)
    try:
        server.serve_forever(poll_interval=0.2)
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
