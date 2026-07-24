from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
import uuid
from pathlib import Path


MODULE_PATH = Path(__file__).parents[1] / "enterprise-rag-channel-contract.py"
SPEC = importlib.util.spec_from_file_location(
    "enterprise_rag_channel_contract", MODULE_PATH
)
assert SPEC and SPEC.loader
CONTRACT = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = CONTRACT
SPEC.loader.exec_module(CONTRACT)


def fixture() -> tuple[dict, dict]:
    batch_id = "contract-test-20260724"
    manifest_documents = []
    state_documents = []
    for format_name, prefix, filename in (
        ("markdown", "MD", "policy-markdown.md"),
        ("text", "TXT", "policy-text.txt"),
        ("pdf", "PDF", "policy-pdf.pdf"),
        ("docx", "DOCX", "policy-docx.docx"),
    ):
        title = f"E2E RAG {format_name} {batch_id}"
        marker = f"{prefix}-{batch_id}"
        manifest_item = {
            "format": format_name,
            "title": title,
            "filename": filename,
            "marker": marker,
        }
        state_item = {
            **manifest_item,
            "document_id": str(uuid.uuid4()),
            "version_id": str(uuid.uuid4()),
            "version_number": 1,
        }
        if format_name == "markdown":
            manifest_item["version_file"] = "policy-markdown-v2.md"
            manifest_item["version_marker"] = f"MD2-{batch_id}"
            state_item["version_file"] = manifest_item["version_file"]
            state_item["version_marker"] = manifest_item["version_marker"]
        manifest_documents.append(manifest_item)
        state_documents.append(state_item)
    manifest = {
        "schema_version": 1,
        "batch_id": batch_id,
        "question": "List the four control codes.",
        "old_version_query": "Report the current Markdown control code.",
        "documents": manifest_documents,
    }
    state = {
        "schema_version": 1,
        "batch_id": batch_id,
        "documents": state_documents,
        "web": {},
    }
    return state, manifest


class ChannelContractTest(unittest.TestCase):
    def write_fixture(self, state: dict, manifest: dict) -> tuple[Path, Path]:
        directory = Path(self.temp.name)
        state_path = directory / "state.json"
        manifest_path = directory / "manifest.json"
        state_path.write_text(json.dumps(state), encoding="utf-8")
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        return state_path, manifest_path

    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()

    def tearDown(self) -> None:
        self.temp.cleanup()

    def test_pre_version_phases_do_not_require_next_version(self) -> None:
        state, manifest = fixture()
        state_path, manifest_path = self.write_fixture(state, manifest)
        for channel in ("openim", "telegram"):
            for phase in ("authorized", "denied", "revoked"):
                contract = CONTRACT.load_contract(
                    channel, phase, state_path, manifest_path
                )
                self.assertEqual(contract.markdown_next_version_id, "")
                self.assertIn(f"[{channel.title() if channel == 'telegram' else 'OpenIM'} E2E", contract.prompt)

    def test_version_phase_requires_immutable_next_version(self) -> None:
        state, manifest = fixture()
        state_path, manifest_path = self.write_fixture(state, manifest)
        with self.assertRaisesRegex(
            CONTRACT.ContractError, "Markdown next version is not a UUID"
        ):
            CONTRACT.load_contract("openim", "version", state_path, manifest_path)

        markdown = state["documents"][0]
        markdown["next_version_id"] = str(uuid.uuid4())
        markdown["next_version_number"] = 2
        state_path, manifest_path = self.write_fixture(state, manifest)
        contract = CONTRACT.load_contract(
            "telegram", "version", state_path, manifest_path
        )
        self.assertEqual(
            contract.markdown_next_version_id, markdown["next_version_id"]
        )

    def test_contract_rejects_dollar_quote_injection(self) -> None:
        state, manifest = fixture()
        manifest["question"] = "unsafe $prompt$ value"
        state_path, manifest_path = self.write_fixture(state, manifest)
        contract = CONTRACT.load_contract(
            "openim", "authorized", state_path, manifest_path
        )
        with self.assertRaisesRegex(CONTRACT.ContractError, "unsafe delimiter"):
            contract.pipe_record()


if __name__ == "__main__":
    unittest.main()
