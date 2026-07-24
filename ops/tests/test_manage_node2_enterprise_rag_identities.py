from __future__ import annotations

import importlib.util
import re
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).parents[1] / "manage-node2-enterprise-rag-identities.py"
SPEC = importlib.util.spec_from_file_location("enterprise_rag_identities", SCRIPT)
assert SPEC and SPEC.loader
identities = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(identities)


class IdentityContractTests(unittest.TestCase):
    def setUp(self) -> None:
        self.contract = identities.build_contract("unit-test-20260724")
        self.state = identities.sanitize_contract(
            self.contract,
            "prepared",
            identities.BASELINE_ACTOR_ID,
        )

    def test_contract_round_trip_removes_passwords_from_state(self) -> None:
        identities.validate_contract(self.contract, require_password=True)
        identities.validate_state(self.state)
        for role in ("authorized", "denied"):
            self.assertNotIn("password", self.state["members"][role])

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "contract.json"
            identities.write_private_json(path.resolve(), self.contract)
            loaded = identities.read_json(path)
        identities.validate_contract(loaded, require_password=True)

    def test_member_and_openim_ids_are_stable_and_distinct(self) -> None:
        authorized = identities.expected_member("authorized")
        denied = identities.expected_member("denied")
        self.assertNotEqual(authorized["member_id"], denied["member_id"])
        self.assertNotEqual(authorized["subject_id"], denied["subject_id"])
        self.assertRegex(authorized["openim_user_id"], re.compile(r"^ent_[a-z2-7]{32}$"))
        self.assertEqual(
            authorized["openim_user_id"],
            identities.expected_member("authorized")["openim_user_id"],
        )

    def test_tampered_identity_is_rejected(self) -> None:
        self.contract["members"]["denied"]["member_id"] = (
            "11111111-1111-4111-8111-111111111111"
        )
        with self.assertRaises(identities.ContractError):
            identities.validate_contract(self.contract, require_password=True)

    def test_keycloak_ownership_requires_acceptance_attributes(self) -> None:
        member = self.contract["members"]["authorized"]
        projection = identities.keycloak_user_payload(member, enabled=True)
        identities.verify_keycloak_ownership(projection, member)
        projection["attributes"]["acceptance_owner"] = ["foreign"]
        with self.assertRaises(identities.ContractError):
            identities.verify_keycloak_ownership(projection, member)

    def test_database_verification_requires_exact_authorization_shape(self) -> None:
        with mock.patch.object(
            identities,
            "run_psql",
            side_effect=[
                "2|2|1|2|1|2",
                "0",
                "2",
            ],
        ):
            identities.verify_database(self.state, require_openim=True)
        with mock.patch.object(
            identities,
            "run_psql",
            side_effect=["2|2|2|2|2|2", "0"],
        ):
            with self.assertRaises(identities.ContractError):
                identities.verify_database(self.state, require_openim=False)

    def test_prepare_preflight_accepts_only_zero_residual_owned_links(self) -> None:
        members = self.contract["members"]
        links = "\n".join(
            sorted(
                f"{members[role]['member_id']}|{members[role]['openim_user_id']}|ready"
                for role in ("authorized", "denied")
            )
        )
        with mock.patch.object(
            identities,
            "run_psql",
            side_effect=["0|0|0|0|0|0|0|0", links],
        ):
            identities.preflight_prepare(self.contract)

    def test_prepare_preflight_rejects_residual_run(self) -> None:
        with mock.patch.object(
            identities,
            "run_psql",
            return_value="0|1|0|0|0|0|0|0",
        ):
            with self.assertRaises(identities.ContractError):
                identities.preflight_prepare(self.contract)

    def test_prepare_preflight_rejects_foreign_openim_link(self) -> None:
        members = self.contract["members"]
        foreign = (
            f"{members['authorized']['member_id']}|foreign-user|ready"
        )
        with mock.patch.object(
            identities,
            "run_psql",
            side_effect=["0|0|0|0|0|0|0|0", foreign],
        ):
            with self.assertRaises(identities.ContractError):
                identities.preflight_prepare(self.contract)

    def test_disable_preflight_rejects_live_fixture_references(self) -> None:
        with mock.patch.object(identities, "run_psql", return_value="0|1|0|0|0|0"):
            with self.assertRaises(identities.ContractError):
                identities.preflight_disable(self.state)

    def test_disable_preflight_accepts_exact_owned_links(self) -> None:
        members = self.state["members"]
        links = "\n".join(
            sorted(
                f"{members[role]['member_id']}|{members[role]['openim_user_id']}|ready"
                for role in ("authorized", "denied")
            )
        )
        with mock.patch.object(
            identities,
            "run_psql",
            side_effect=["0|0|0|0|0|0", links],
        ):
            identities.preflight_disable(self.state)

    def test_memory_isolation_preflight_requires_zero_state(self) -> None:
        with mock.patch.object(identities, "run_psql", return_value="0|0"):
            identities.assert_no_acceptance_memory_jobs(self.state)
        with mock.patch.object(identities, "run_psql", return_value="1|0"):
            with self.assertRaises(identities.ContractError):
                identities.assert_no_acceptance_memory_jobs(self.state)


if __name__ == "__main__":
    unittest.main()
