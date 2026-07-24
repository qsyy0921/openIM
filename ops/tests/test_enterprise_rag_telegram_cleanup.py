from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).parents[1] / "enterprise-rag-telegram-cleanup.py"
SPEC = importlib.util.spec_from_file_location(
    "enterprise_rag_telegram_cleanup", MODULE_PATH
)
assert SPEC and SPEC.loader
CLEANUP = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = CLEANUP
SPEC.loader.exec_module(CLEANUP)


def valid_result() -> dict:
    chat_id = 123456789
    channel: dict[str, object] = {
        "bootstraps": [
            {"chat_id": chat_id, "message_id": 101},
            {"chat_id": chat_id, "message_id": 104},
            {"chat_id": chat_id, "message_id": 107},
            {"chat_id": chat_id, "message_id": 110},
        ]
    }
    for index, phase in enumerate(CLEANUP.PHASES):
        source_message = 102 + index * 3
        channel[phase] = {
            "source_server_msg_id": f"telegram:{chat_id}:{source_message}",
            "external_message_id": str(source_message + 1),
        }
    return {
        "schema_version": 1,
        "batch_id": "telegram-cleanup-20260724",
        "telegram": channel,
    }


class TelegramCleanupTest(unittest.TestCase):
    def test_collects_one_exact_chat_and_bounded_message_set(self) -> None:
        message_set = CLEANUP.collect_message_set(valid_result())
        self.assertEqual(message_set.chat_id, 123456789)
        self.assertEqual(len(message_set.message_ids), 12)
        self.assertEqual(message_set.message_ids, tuple(sorted(message_set.message_ids)))
        self.assertTrue(message_set.digest.startswith("sha256:"))
        self.assertEqual(len(message_set.digest), 71)

    def test_missing_phase_fails_closed(self) -> None:
        value = valid_result()
        del value["telegram"]["denied"]
        with self.assertRaisesRegex(
            CLEANUP.CleanupError, "all four Telegram acceptance phases"
        ):
            CLEANUP.collect_message_set(value)

    def test_cross_chat_message_fails_closed(self) -> None:
        value = valid_result()
        value["telegram"]["version"]["source_server_msg_id"] = (
            "telegram:987654321:111"
        )
        with self.assertRaisesRegex(CLEANUP.CleanupError, "multiple chats"):
            CLEANUP.collect_message_set(value)

    def test_malformed_external_message_fails_closed(self) -> None:
        value = valid_result()
        value["telegram"]["authorized"]["external_message_id"] = "0"
        with self.assertRaisesRegex(
            CLEANUP.CleanupError, "authorized message identifiers"
        ):
            CLEANUP.collect_message_set(value)


if __name__ == "__main__":
    unittest.main()
