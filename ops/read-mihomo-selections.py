#!/usr/bin/env python3
"""Read active Mihomo proxy-group selections without exposing provider config."""

from __future__ import annotations

import argparse
import http.client
import json
import socket
import urllib.request
from collections.abc import Mapping
from typing import Any


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, socket_path: str, timeout: float) -> None:
        super().__init__("localhost", timeout=timeout)
        self.socket_path = socket_path

    def connect(self) -> None:
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.socket_path)


def read_from_unix_socket(socket_path: str, timeout: float) -> bytes:
    connection = UnixHTTPConnection(socket_path, timeout)
    try:
        connection.request("GET", "/proxies")
        response = connection.getresponse()
        body = response.read()
        if response.status != 200:
            raise RuntimeError(f"Mihomo controller returned HTTP {response.status}")
        return body
    finally:
        connection.close()


def read_from_url(url: str, timeout: float) -> bytes:
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return response.read()


def extract_selections(
    payload: Mapping[str, Any], *, include_dynamic: bool
) -> dict[str, str]:
    proxies = payload.get("proxies")
    if not isinstance(proxies, Mapping):
        raise ValueError("Mihomo response does not contain a proxies object")

    selections: dict[str, str] = {}
    for name, value in proxies.items():
        if not isinstance(name, str) or not isinstance(value, Mapping):
            continue
        if not include_dynamic and value.get("type") != "Selector":
            continue
        selected = value.get("now")
        members = value.get("all")
        if isinstance(selected, str) and selected and isinstance(members, list):
            selections[name] = selected
    return dict(sorted(selections.items()))


def main() -> int:
    parser = argparse.ArgumentParser()
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--unix-socket")
    source.add_argument("--url")
    parser.add_argument("--timeout", type=float, default=5.0)
    parser.add_argument(
        "--include-dynamic",
        action="store_true",
        help="include URL-test and other dynamic groups, not only selectors",
    )
    args = parser.parse_args()

    if args.unix_socket:
        raw = read_from_unix_socket(args.unix_socket, args.timeout)
    else:
        raw = read_from_url(args.url, args.timeout)
    payload = json.loads(raw.decode("utf-8"))
    print(
        json.dumps(
            extract_selections(payload, include_dynamic=args.include_dynamic),
            ensure_ascii=False,
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
