#!/usr/bin/env python3
"""Validate and execute exact Telegram message cleanup for RAG acceptance."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import ssl
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path


BATCH_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]{7,63}$")
SOURCE_MESSAGE_PATTERN = re.compile(
    r"^telegram:(-?[1-9][0-9]{4,19}):([1-9][0-9]*)$"
)
MESSAGE_ID_PATTERN = re.compile(r"^[1-9][0-9]*$")
API_BASE_PATTERN = re.compile(r"^https://[A-Za-z0-9.-]+(?::[0-9]+)?$")
TOKEN_PATTERN = re.compile(r"^[0-9]{8,12}:[A-Za-z0-9_-]{30,}$")
PROXY_PATTERN = re.compile(r"^http://127\.0\.0\.1:[0-9]{1,5}$")
PHASES = ("authorized", "denied", "revoked", "version")


class CleanupError(ValueError):
    pass


@dataclass(frozen=True)
class MessageSet:
    batch_id: str
    chat_id: int
    message_ids: tuple[int, ...]
    digest: str


def read_json(path: Path) -> dict:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise CleanupError("Telegram result root is malformed")
    return value


def collect_message_set(value: dict) -> MessageSet:
    batch_id = value.get("batch_id")
    if value.get("schema_version") != 1 or not isinstance(
        batch_id, str
    ) or not BATCH_PATTERN.fullmatch(batch_id):
        raise CleanupError("Telegram result contract is malformed")
    channel = value.get("telegram")
    if not isinstance(channel, dict):
        raise CleanupError("Telegram result contract is incomplete")
    if any(not isinstance(channel.get(phase), dict) for phase in PHASES):
        raise CleanupError(
            "all four Telegram acceptance phases must complete before cleanup"
        )

    messages: list[int] = []
    chat_ids: set[int] = set()
    bootstraps = channel.get("bootstraps")
    if not isinstance(bootstraps, list) or len(bootstraps) < 1:
        raise CleanupError("Telegram bootstrap records are incomplete")
    for item in bootstraps:
        if not isinstance(item, dict):
            raise CleanupError("Telegram bootstrap record is malformed")
        chat_id, message_id = item.get("chat_id"), item.get("message_id")
        if (
            not isinstance(chat_id, int)
            or chat_id == 0
            or not isinstance(message_id, int)
            or message_id <= 0
        ):
            raise CleanupError("Telegram bootstrap identifiers are malformed")
        chat_ids.add(chat_id)
        messages.append(message_id)

    for phase in PHASES:
        record = channel[phase]
        source = SOURCE_MESSAGE_PATTERN.fullmatch(
            str(record.get("source_server_msg_id", ""))
        )
        external = str(record.get("external_message_id", ""))
        if source is None or MESSAGE_ID_PATTERN.fullmatch(external) is None:
            raise CleanupError(f"Telegram {phase} message identifiers are malformed")
        chat_ids.add(int(source.group(1)))
        messages.extend((int(source.group(2)), int(external)))

    if len(chat_ids) != 1:
        raise CleanupError("Telegram acceptance messages span multiple chats")
    message_ids = tuple(sorted(set(messages)))
    if len(message_ids) < 8 or len(message_ids) > 100:
        raise CleanupError(
            "Telegram cleanup message set is incomplete or exceeds the API bound"
        )
    chat_id = next(iter(chat_ids))
    digest = "sha256:" + hashlib.sha256(
        json.dumps([chat_id, message_ids], separators=(",", ":")).encode()
    ).hexdigest()
    return MessageSet(
        batch_id=batch_id,
        chat_id=chat_id,
        message_ids=message_ids,
        digest=digest,
    )


def load_environment(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if line and not line.startswith("#") and "=" in line:
            key, item = line.split("=", 1)
            values[key.strip()] = item.strip()
    return values


def write_result(path: Path, value: dict) -> None:
    ownership = path.stat()
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(
        json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    temporary.chmod(0o600)
    os.chown(temporary, ownership.st_uid, ownership.st_gid)
    temporary.replace(path)


def execute_cleanup(
    result_path: Path,
    platform_env: Path,
    credential_path: Path,
    proxy_url: str,
) -> int:
    value = read_json(result_path)
    message_set = collect_message_set(value)
    channel = value["telegram"]
    cleanup = channel.get("cleanup")
    if isinstance(cleanup, dict):
        if cleanup.get("message_ids_digest") != message_set.digest:
            raise CleanupError(
                "Telegram cleanup record differs from the current message set"
            )
        if cleanup.get("state") == "completed":
            return len(message_set.message_ids)
        if cleanup.get("state") == "prepared":
            raise CleanupError(
                "a previous Telegram deleteMessages outcome remains uncertain"
            )
        raise CleanupError("Telegram cleanup state is malformed")

    if not PROXY_PATTERN.fullmatch(proxy_url):
        raise CleanupError("Telegram cleanup proxy must be loopback-only")
    runtime = load_environment(platform_env)
    api_base = runtime.get("PLATFORM_TELEGRAM_API_BASE_URL", "")
    if not API_BASE_PATTERN.fullmatch(api_base):
        raise CleanupError("Telegram API base URL is missing or malformed")
    token = credential_path.read_text(encoding="utf-8").strip()
    if not TOKEN_PATTERN.fullmatch(token):
        raise CleanupError("Telegram Bot credential is malformed")

    channel["cleanup"] = {
        "state": "prepared",
        "message_ids_digest": message_set.digest,
    }
    write_result(result_path, value)

    payload = json.dumps(
        {
            "chat_id": message_set.chat_id,
            "message_ids": message_set.message_ids,
        },
        separators=(",", ":"),
    ).encode()
    request = urllib.request.Request(
        f"{api_base}/bot{token}/deleteMessages",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({"http": proxy_url, "https": proxy_url}),
        urllib.request.HTTPSHandler(context=ssl.create_default_context()),
    )
    try:
        with opener.open(request, timeout=30) as response:
            body = response.read(1 << 20)
    except (urllib.error.HTTPError, urllib.error.URLError) as error:
        raise CleanupError(
            "Telegram deleteMessages transport or API call failed with uncertain outcome"
        ) from error
    finally:
        token = ""
    try:
        response_value = json.loads(body)
    except json.JSONDecodeError as error:
        raise CleanupError(
            "Telegram deleteMessages returned malformed JSON with uncertain outcome"
        ) from error
    if (
        not isinstance(response_value, dict)
        or response_value.get("ok") is not True
        or response_value.get("result") is not True
    ):
        raise CleanupError("Telegram deleteMessages did not confirm deletion")

    value = read_json(result_path)
    cleanup = value.get("telegram", {}).get("cleanup")
    expected = {
        "state": "prepared",
        "message_ids_digest": message_set.digest,
    }
    if cleanup != expected:
        raise CleanupError("Telegram cleanup state changed during the external request")
    cleanup["state"] = "completed"
    write_result(result_path, value)
    return len(message_set.message_ids)


def main(arguments: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    contract = commands.add_parser("contract")
    contract.add_argument("result", type=Path)
    execute = commands.add_parser("execute")
    execute.add_argument("result", type=Path)
    execute.add_argument("platform_env", type=Path)
    execute.add_argument("credential", type=Path)
    execute.add_argument("proxy_url")
    parsed = parser.parse_args(arguments)
    try:
        if parsed.command == "contract":
            message_set = collect_message_set(read_json(parsed.result))
            print(
                "enterprise_rag_telegram_cleanup_contract=valid"
                f" count={len(message_set.message_ids)}"
                f" digest={message_set.digest}"
            )
        else:
            count = execute_cleanup(
                parsed.result,
                parsed.platform_env,
                parsed.credential,
                parsed.proxy_url,
            )
            print(f"enterprise_rag_telegram_messages=deleted count={count}")
    except (CleanupError, json.JSONDecodeError, OSError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
