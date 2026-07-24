#!/usr/bin/env python3
"""Create, verify, and disable the two isolated Node2 enterprise RAG identities."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import secrets
import stat
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path
from typing import Any


SCHEMA_VERSION = 1
TENANT_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT_EXTERNAL_ID = "tenant-local"
BASELINE_ACTOR_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
DEVICE_ID = "ubuntu-web"
PLATFORM_ID = 5
KEYCLOAK_BASE_URL = "http://127.0.0.1:18081/auth"
KEYCLOAK_REALM = "platform"
KEYCLOAK_ADMIN_REALM = "master"
KEYCLOAK_ADMIN_USERNAME = "local-admin"
ACCEPTANCE_OWNER = "openim-enterprise-rag-e2e"
POSTGRES_CONTAINER = "openim-platform-local-postgres-1"
PLATFORM_ENV = "/etc/openim-platform/platform.env"
MEMORY_EXTRACTOR_UNIT = "openim-memory-extractor.service"
BATCH_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]{7,63}$")
PASSWORD_PATTERN = re.compile(r"^[A-Za-z0-9_-]{40,96}$")
SAFE_TEXT_PATTERN = re.compile(r"^[A-Za-z0-9 ._-]{1,120}$")


class ContractError(RuntimeError):
    """The acceptance contract or external state is unsafe."""


def deterministic_uuid(label: str) -> str:
    return str(uuid.uuid5(uuid.UUID(TENANT_ID), f"{ACCEPTANCE_OWNER}:{label}"))


def deterministic_openim_user_id(member_id: str) -> str:
    digest = hashlib.sha256(f"{TENANT_ID}\x00{member_id}".encode()).digest()[:20]
    return "ent_" + base64.b32encode(digest).decode().rstrip("=").lower()


def expected_member(role: str) -> dict[str, Any]:
    if role not in {"authorized", "denied"}:
        raise ContractError(f"unsupported enterprise RAG identity role {role!r}")
    member_id = deterministic_uuid(f"member:{role}")
    label = "A" if role == "authorized" else "B"
    return {
        "role": role,
        "member_id": member_id,
        "subject_id": deterministic_uuid(f"oidc:{role}"),
        "username": f"rag-e2e-{role}",
        "display_name": f"Enterprise RAG Acceptance {label}",
        "device_id": DEVICE_ID,
        "platform_id": PLATFORM_ID,
        "openim_user_id": deterministic_openim_user_id(member_id),
    }


def build_contract(batch_id: str) -> dict[str, Any]:
    if not BATCH_PATTERN.fullmatch(batch_id):
        raise ContractError("batch ID must contain 8-64 safe characters")
    members: dict[str, Any] = {}
    for role in ("authorized", "denied"):
        members[role] = {
            **expected_member(role),
            "password": secrets.token_urlsafe(36),
        }
    return {
        "schema_version": SCHEMA_VERSION,
        "batch_id": batch_id,
        "tenant_id": TENANT_ID,
        "members": members,
    }


def validate_contract(value: Any, *, require_password: bool) -> dict[str, Any]:
    if not isinstance(value, dict) or value.get("schema_version") != SCHEMA_VERSION:
        raise ContractError("enterprise RAG identity contract schema is invalid")
    batch_id = value.get("batch_id")
    if not isinstance(batch_id, str) or not BATCH_PATTERN.fullmatch(batch_id):
        raise ContractError("enterprise RAG identity contract batch ID is invalid")
    if value.get("tenant_id") != TENANT_ID:
        raise ContractError("enterprise RAG identity contract tenant is invalid")
    members = value.get("members")
    if not isinstance(members, dict) or set(members) != {"authorized", "denied"}:
        raise ContractError("enterprise RAG identity contract must contain A and B")
    for role in ("authorized", "denied"):
        actual = members.get(role)
        expected = expected_member(role)
        if not isinstance(actual, dict):
            raise ContractError(f"{role} identity is malformed")
        for key, expected_value in expected.items():
            if actual.get(key) != expected_value:
                raise ContractError(f"{role} identity field {key} is not deterministic")
        password = actual.get("password")
        if require_password and (
            not isinstance(password, str) or not PASSWORD_PATTERN.fullmatch(password)
        ):
            raise ContractError(f"{role} identity password is missing or malformed")
        if not require_password and "password" in actual:
            raise ContractError("sanitized identity state must not contain passwords")
    return value


def read_json(path: Path) -> dict[str, Any]:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ContractError(f"cannot read JSON contract {path.name}: {exc}") from exc


def write_private_json(path: Path, value: dict[str, Any]) -> None:
    if not path.is_absolute():
        raise ContractError("identity contract output path must be absolute")
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(value, indent=2, sort_keys=True) + "\n"
    fd, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=path.parent
    )
    temporary = Path(temporary_name)
    try:
        os.fchmod(fd, stat.S_IRUSR | stat.S_IWUSR)
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as stream:
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        temporary.replace(path)
        try:
            path.chmod(stat.S_IRUSR | stat.S_IWUSR)
        except OSError:
            if os.name != "nt":
                raise
    finally:
        temporary.unlink(missing_ok=True)


def require_private_file(path: Path) -> None:
    if not path.is_absolute() or not path.is_file():
        raise ContractError("identity credential path must be an absolute regular file")
    if os.name != "nt" and stat.S_IMODE(path.stat().st_mode) & 0o077:
        raise ContractError("identity credential file must not be group/world accessible")


def sanitize_contract(contract: dict[str, Any], status: str, actor_id: str) -> dict[str, Any]:
    return {
        "schema_version": SCHEMA_VERSION,
        "batch_id": contract["batch_id"],
        "tenant_id": TENANT_ID,
        "status": status,
        "actor_member_id": actor_id,
        "members": {
            role: {
                key: value
                for key, value in contract["members"][role].items()
                if key != "password"
            }
            for role in ("authorized", "denied")
        },
    }


def load_env(path: Path) -> dict[str, str]:
    if not path.is_file():
        raise ContractError(f"required host environment is missing: {path}")
    result: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        result[key.strip()] = value.strip()
    return result


def required_env(env: dict[str, str], key: str) -> str:
    value = env.get(key, "")
    if not value or any(character.isspace() for character in value):
        raise ContractError(f"required host environment field {key} is missing or malformed")
    return value


def json_request(
    method: str,
    url: str,
    *,
    payload: Any | None = None,
    token: str = "",
    form: dict[str, str] | None = None,
) -> Any:
    data: bytes | None = None
    headers = {"Accept": "application/json"}
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif payload is not None:
        data = json.dumps(payload, separators=(",", ":")).encode()
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            body = response.read(1 << 20)
    except urllib.error.HTTPError as exc:
        raise ContractError(
            f"loopback API rejected {method} {urllib.parse.urlsplit(url).path}: HTTP {exc.code}"
        ) from exc
    except urllib.error.URLError as exc:
        raise ContractError(
            f"loopback API unavailable for {urllib.parse.urlsplit(url).path}"
        ) from exc
    if not body:
        return None
    try:
        return json.loads(body)
    except json.JSONDecodeError as exc:
        raise ContractError("loopback API returned malformed JSON") from exc


def keycloak_admin_token(admin_password: str) -> str:
    response = json_request(
        "POST",
        f"{KEYCLOAK_BASE_URL}/realms/{KEYCLOAK_ADMIN_REALM}/protocol/openid-connect/token",
        form={
            "grant_type": "password",
            "client_id": "admin-cli",
            "username": KEYCLOAK_ADMIN_USERNAME,
            "password": admin_password,
        },
    )
    token = response.get("access_token") if isinstance(response, dict) else None
    if not isinstance(token, str) or len(token) < 32:
        raise ContractError("Keycloak returned invalid administrator session material")
    return token


def keycloak_users_url() -> str:
    return f"{KEYCLOAK_BASE_URL}/admin/realms/{KEYCLOAK_REALM}/users"


def find_keycloak_user(token: str, username: str) -> dict[str, Any] | None:
    query = urllib.parse.urlencode(
        {"username": username, "exact": "true", "briefRepresentation": "false"}
    )
    users = json_request("GET", f"{keycloak_users_url()}?{query}", token=token)
    if not isinstance(users, list) or len(users) > 1:
        raise ContractError(f"Keycloak username {username} is ambiguous")
    if not users:
        return None
    if not isinstance(users[0], dict):
        raise ContractError("Keycloak returned a malformed user projection")
    user_id = users[0].get("id")
    if not isinstance(user_id, str):
        raise ContractError("Keycloak returned a user without an ID")
    user = json_request("GET", f"{keycloak_users_url()}/{user_id}", token=token)
    if not isinstance(user, dict):
        raise ContractError("Keycloak returned a malformed full user projection")
    return user


def keycloak_user_payload(member: dict[str, Any], *, enabled: bool) -> dict[str, Any]:
    return {
        "id": member["subject_id"],
        "username": member["username"],
        "enabled": enabled,
        "emailVerified": True,
        "firstName": "Enterprise RAG",
        "lastName": "Acceptance",
        "attributes": {
            "tenant_id": [TENANT_EXTERNAL_ID],
            "acceptance_owner": [ACCEPTANCE_OWNER],
            "acceptance_role": [member["role"]],
        },
    }


def verify_keycloak_ownership(user: dict[str, Any], member: dict[str, Any]) -> None:
    attributes = user.get("attributes")
    if (
        user.get("id") != member["subject_id"]
        or user.get("username") != member["username"]
        or not isinstance(attributes, dict)
        or attributes.get("tenant_id") != [TENANT_EXTERNAL_ID]
        or attributes.get("acceptance_owner") != [ACCEPTANCE_OWNER]
        or attributes.get("acceptance_role") != [member["role"]]
    ):
        raise ContractError(
            f"Keycloak user {member['username']} is not owned by this acceptance fixture"
        )


def ensure_keycloak_user(token: str, member: dict[str, Any]) -> None:
    user = find_keycloak_user(token, member["username"])
    if user is None:
        payload = {
            **keycloak_user_payload(member, enabled=True),
            "credentials": [
                {
                    "type": "password",
                    "value": member["password"],
                    "temporary": False,
                }
            ],
        }
        json_request("POST", keycloak_users_url(), payload=payload, token=token)
        user = find_keycloak_user(token, member["username"])
        if user is None:
            raise ContractError("Keycloak did not persist the acceptance user")
    verify_keycloak_ownership(user, member)
    json_request(
        "PUT",
        f"{keycloak_users_url()}/{member['subject_id']}",
        payload=keycloak_user_payload(member, enabled=True),
        token=token,
    )
    json_request(
        "PUT",
        f"{keycloak_users_url()}/{member['subject_id']}/reset-password",
        payload={
            "type": "password",
            "value": member["password"],
            "temporary": False,
        },
        token=token,
    )


def disable_keycloak_user(token: str, member: dict[str, Any]) -> None:
    user = find_keycloak_user(token, member["username"])
    if user is None:
        raise ContractError(f"Keycloak acceptance user {member['username']} is missing")
    verify_keycloak_ownership(user, member)
    json_request(
        "PUT",
        f"{keycloak_users_url()}/{member['subject_id']}",
        payload=keycloak_user_payload(member, enabled=False),
        token=token,
    )


def run_psql(sql: str) -> str:
    command = [
        "docker",
        "exec",
        "-i",
        POSTGRES_CONTAINER,
        "psql",
        "-At",
        "-F",
        "|",
        "-v",
        "ON_ERROR_STOP=1",
        "-U",
        "platform",
        "-d",
        "platform",
    ]
    completed = subprocess.run(
        command,
        input=sql,
        text=True,
        capture_output=True,
        check=False,
    )
    if completed.returncode != 0:
        detail = completed.stderr.strip().splitlines()
        raise ContractError(
            f"PostgreSQL fixture operation failed: {detail[-1] if detail else 'unknown error'}"
        )
    return completed.stdout.strip()


def quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def find_fixture_actor() -> str:
    actor = run_psql(
        f"""
SELECT role.member_id::text
FROM identity.member_roles AS role
JOIN identity.members AS member
  ON member.tenant_id=role.tenant_id AND member.id=role.member_id
WHERE role.tenant_id={quote(TENANT_ID)}::uuid
  AND role.role='platform_admin'
  AND member.status='active'
  AND role.member_id={quote(BASELINE_ACTOR_ID)}::uuid;
"""
    )
    if actor != BASELINE_ACTOR_ID:
        raise ContractError("authoritative Node2 platform administrator is missing or inactive")
    return actor


def preflight_prepare(contract: dict[str, Any]) -> None:
    members = contract["members"]
    a = members["authorized"]
    b = members["denied"]
    member_ids = (
        f"{quote(a['member_id'])}::uuid,"
        f"{quote(b['member_id'])}::uuid"
    )
    row = run_psql(
        f"""
SELECT
  (SELECT count(*) FROM authz.document_grants WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM agent.runs WHERE principal_member_id IN ({member_ids})),
  (SELECT count(*) FROM channel.telegram_principals WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM channel.telegram_link_challenges WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM memory.extraction_jobs WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM memory.streams WHERE owner_member_id IN ({member_ids})),
  (SELECT count(*) FROM identity.member_roles
    WHERE tenant_id={quote(TENANT_ID)}::uuid
      AND member_id IN ({member_ids})
      AND NOT (
        member_id={quote(a["member_id"])}::uuid
        AND role='platform_admin'
      )),
  (SELECT count(*) FROM capability.member_grants
    WHERE tenant_id={quote(TENANT_ID)}::uuid
      AND member_id IN ({member_ids})
      AND permission<>'knowledge:read');
"""
    )
    if row != "0|0|0|0|0|0|0|0":
        raise ContractError(
            f"enterprise RAG identities retain incompatible state; prepare preflight is {row}"
        )

    links = run_psql(
        f"""
SELECT member_id::text,openim_user_id,provisioning_state
FROM identity.identity_links
WHERE tenant_id={quote(TENANT_ID)}::uuid
  AND member_id IN ({member_ids})
ORDER BY member_id;
"""
    ).splitlines()
    expected = {
        f"{members[role]['member_id']}|{members[role]['openim_user_id']}|ready"
        for role in ("authorized", "denied")
    }
    if any(link not in expected for link in links):
        raise ContractError(
            "existing OpenIM identity links are not owned by the acceptance fixture"
        )


def prepare_database(contract: dict[str, Any], issuer: str, actor_id: str) -> None:
    if not issuer.startswith("https://") or any(character.isspace() for character in issuer):
        raise ContractError("OIDC issuer must be an explicit HTTPS URL")
    batch = contract["batch_id"]
    members = contract["members"]
    a = members["authorized"]
    b = members["denied"]
    for member in (a, b):
        for field in ("username", "display_name", "device_id"):
            if not SAFE_TEXT_PATTERN.fullmatch(member[field]):
                raise ContractError(f"unsafe identity field {field}")
    sql = f"""
BEGIN;
DO $acceptance$
DECLARE conflict_count integer;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM identity.tenants
    WHERE id={quote(TENANT_ID)}::uuid
      AND external_id={quote(TENANT_EXTERNAL_ID)}
      AND status='active'
  ) THEN
    RAISE EXCEPTION 'enterprise RAG acceptance tenant is missing or inactive';
  END IF;
  SELECT count(*) INTO conflict_count
  FROM identity.members
  WHERE id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
    AND (
      tenant_id<>{quote(TENANT_ID)}::uuid
      OR issuer<>{quote(issuer)}
      OR (id={quote(a["member_id"])}::uuid AND subject<>{quote(a["subject_id"])})
      OR (id={quote(b["member_id"])}::uuid AND subject<>{quote(b["subject_id"])})
    );
  IF conflict_count<>0 THEN
    RAISE EXCEPTION 'enterprise RAG acceptance member identity conflicts with existing state';
  END IF;
  SELECT count(*) INTO conflict_count
  FROM identity.members
  WHERE tenant_id={quote(TENANT_ID)}::uuid
    AND issuer={quote(issuer)}
    AND subject IN ({quote(a["subject_id"])},{quote(b["subject_id"])})
    AND id NOT IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid);
  IF conflict_count<>0 THEN
    RAISE EXCEPTION 'enterprise RAG acceptance OIDC subjects are already owned';
  END IF;
END
$acceptance$;

INSERT INTO identity.members(id,tenant_id,issuer,subject,display_name,status)
VALUES
  ({quote(a["member_id"])}::uuid,{quote(TENANT_ID)}::uuid,{quote(issuer)},
   {quote(a["subject_id"])},{quote(a["display_name"])},'active'),
  ({quote(b["member_id"])}::uuid,{quote(TENANT_ID)}::uuid,{quote(issuer)},
   {quote(b["subject_id"])},{quote(b["display_name"])},'active')
ON CONFLICT (id) DO UPDATE
SET display_name=EXCLUDED.display_name,status='active',updated_at=now();

INSERT INTO identity.member_devices(member_id,device_id,platform_id,status)
VALUES
  ({quote(a["member_id"])}::uuid,{quote(DEVICE_ID)},{PLATFORM_ID},'active'),
  ({quote(b["member_id"])}::uuid,{quote(DEVICE_ID)},{PLATFORM_ID},'active')
ON CONFLICT (member_id,device_id,platform_id) DO UPDATE
SET status='active',updated_at=now();

WITH inserted AS (
  INSERT INTO identity.member_roles(tenant_id,member_id,role,granted_by_member_id)
  VALUES ({quote(TENANT_ID)}::uuid,{quote(a["member_id"])}::uuid,'platform_admin',
          {quote(actor_id)}::uuid)
  ON CONFLICT DO NOTHING
  RETURNING tenant_id,member_id
)
INSERT INTO audit.identity_admin_events(
  tenant_id,actor_member_id,subject_member_id,event_type,evidence)
SELECT tenant_id,{quote(actor_id)}::uuid,member_id,'role_granted',
       jsonb_build_object('role','platform_admin','acceptance_batch',{quote(batch)})
FROM inserted;

WITH inserted AS (
  INSERT INTO capability.member_grants(tenant_id,member_id,permission)
  VALUES
    ({quote(TENANT_ID)}::uuid,{quote(a["member_id"])}::uuid,'knowledge:read'),
    ({quote(TENANT_ID)}::uuid,{quote(b["member_id"])}::uuid,'knowledge:read')
  ON CONFLICT DO NOTHING
  RETURNING tenant_id,member_id,permission
)
INSERT INTO audit.capability_admin_events(
  tenant_id,actor_member_id,subject_member_id,event_type,evidence)
SELECT tenant_id,{quote(actor_id)}::uuid,member_id,'member_grant_added',
       jsonb_build_object('permission',permission,'acceptance_batch',{quote(batch)})
FROM inserted;
COMMIT;
"""
    run_psql(sql)


def verify_database(state: dict[str, Any], *, require_openim: bool) -> None:
    members = state["members"]
    a = members["authorized"]
    b = members["denied"]
    expected = "2|2|1|2|1|2"
    row = run_psql(
        f"""
SELECT
  (SELECT count(*) FROM identity.members
    WHERE id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
      AND tenant_id={quote(TENANT_ID)}::uuid AND status='active'),
  (SELECT count(*) FROM identity.member_devices
    WHERE member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
      AND device_id={quote(DEVICE_ID)} AND platform_id={PLATFORM_ID} AND status='active'),
  (SELECT count(*) FROM identity.member_roles
    WHERE tenant_id={quote(TENANT_ID)}::uuid
      AND member_id={quote(a["member_id"])}::uuid AND role='platform_admin'),
  (SELECT count(*) FROM capability.member_grants
    WHERE tenant_id={quote(TENANT_ID)}::uuid
      AND member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
      AND permission='knowledge:read'),
  (SELECT count(*) FROM identity.member_roles
    WHERE tenant_id={quote(TENANT_ID)}::uuid
      AND member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)),
  (SELECT count(*) FROM capability.member_grants
    WHERE tenant_id={quote(TENANT_ID)}::uuid
      AND member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid));
"""
    )
    if row != expected:
        raise ContractError(f"enterprise RAG identity database contract is {row}, expected {expected}")
    denied_roles = run_psql(
        f"""
SELECT count(*) FROM identity.member_roles
WHERE tenant_id={quote(TENANT_ID)}::uuid AND member_id={quote(b["member_id"])}::uuid;
"""
    )
    if denied_roles != "0":
        raise ContractError("denied acceptance member unexpectedly has an administrative role")
    if require_openim:
        links = run_psql(
            f"""
SELECT count(*) FROM identity.identity_links
WHERE tenant_id={quote(TENANT_ID)}::uuid
  AND provisioning_state='ready'
  AND (
    (member_id={quote(a["member_id"])}::uuid AND openim_user_id={quote(a["openim_user_id"])})
    OR
    (member_id={quote(b["member_id"])}::uuid AND openim_user_id={quote(b["openim_user_id"])})
  );
"""
        )
        if links != "2":
            raise ContractError("both isolated OpenIM identities have not been provisioned")


def openim_request(base_url: str, path: str, payload: dict[str, Any], token: str = "") -> Any:
    headers = {"Content-Type": "application/json", "operationID": secrets.token_hex(16)}
    if token:
        headers["token"] = token
    request = urllib.request.Request(
        f"{base_url.rstrip('/')}{path}",
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers=headers,
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            body = response.read(1 << 20)
    except (urllib.error.HTTPError, urllib.error.URLError) as exc:
        raise ContractError(f"OpenIM loopback call failed for {path}") from exc
    try:
        value = json.loads(body)
    except json.JSONDecodeError as exc:
        raise ContractError("OpenIM returned malformed JSON") from exc
    if not isinstance(value, dict) or value.get("errCode") != 0:
        code = value.get("errCode") if isinstance(value, dict) else "malformed"
        raise ContractError(f"OpenIM rejected {path} with error code {code}")
    return value.get("data")


def clear_isolated_openim_messages(state: dict[str, Any], env: dict[str, str]) -> None:
    base_url = required_env(env, "PLATFORM_OPENIM_API_URL")
    if not base_url.startswith("http://127.0.0.1:"):
        raise ContractError("OpenIM acceptance cleanup must use the Node2 loopback API")
    secret = required_env(env, "PLATFORM_OPENIM_SECRET")
    admin_user = required_env(env, "PLATFORM_OPENIM_ADMIN_USER_ID")
    token_data = openim_request(
        base_url,
        "/auth/get_admin_token",
        {"secret": secret, "userID": admin_user},
    )
    token = token_data.get("token") if isinstance(token_data, dict) else None
    if not isinstance(token, str) or len(token) < 32:
        raise ContractError("OpenIM returned invalid administrator session material")
    members = state["members"]
    user_ids = [members[role]["openim_user_id"] for role in ("authorized", "denied")]
    users = openim_request(
        base_url,
        "/user/get_users_info",
        {"userIDs": user_ids},
        token,
    )
    info = users.get("usersInfo") if isinstance(users, dict) else None
    by_id = {
        item.get("userID"): item
        for item in info
        if isinstance(item, dict) and isinstance(item.get("userID"), str)
    } if isinstance(info, list) else {}
    for role in ("authorized", "denied"):
        member = members[role]
        marker = f"platform-identity:{TENANT_ID}/{member['member_id']}"
        if by_id.get(member["openim_user_id"], {}).get("ex") != marker:
            raise ContractError(
                f"OpenIM {role} identity is not owned by this acceptance fixture"
            )
    for user_id in user_ids:
        openim_request(
            base_url,
            "/auth/force_logout",
            {"userID": user_id, "platformID": PLATFORM_ID},
            token,
        )
        openim_request(
            base_url,
            "/msg/user_clear_all_msg",
            {
                "userID": user_id,
                "deleteSyncOpt": {"isSyncSelf": True, "isSyncOther": True},
            },
            token,
        )


def preflight_disable(state: dict[str, Any]) -> None:
    members = state["members"]
    member_ids = (
        f"{quote(members['authorized']['member_id'])}::uuid,"
        f"{quote(members['denied']['member_id'])}::uuid"
    )
    row = run_psql(
        f"""
SELECT
  (SELECT count(*) FROM authz.document_grants WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM agent.runs WHERE principal_member_id IN ({member_ids})),
  (SELECT count(*) FROM channel.telegram_principals WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM channel.telegram_link_challenges WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM memory.extraction_jobs WHERE member_id IN ({member_ids})),
  (SELECT count(*) FROM memory.streams WHERE owner_member_id IN ({member_ids}));
"""
    )
    if row != "0|0|0|0|0|0":
        raise ContractError(
            f"enterprise RAG identities still own acceptance state; cleanup preflight is {row}"
        )
    links = run_psql(
        f"""
SELECT member_id::text,openim_user_id,provisioning_state
FROM identity.identity_links
WHERE member_id IN ({member_ids})
ORDER BY member_id;
"""
    ).splitlines()
    expected = sorted(
        f"{members[role]['member_id']}|{members[role]['openim_user_id']}|ready"
        for role in ("authorized", "denied")
    )
    if sorted(links) != expected:
        raise ContractError("isolated OpenIM identity links do not match the acceptance state")


def disable_database(state: dict[str, Any]) -> None:
    batch = state["batch_id"]
    actor_id = state["actor_member_id"]
    a = state["members"]["authorized"]
    b = state["members"]["denied"]
    sql = f"""
BEGIN;
WITH removed AS (
  DELETE FROM identity.member_roles
  WHERE tenant_id={quote(TENANT_ID)}::uuid
    AND member_id={quote(a["member_id"])}::uuid
    AND role='platform_admin'
  RETURNING tenant_id,member_id
)
INSERT INTO audit.identity_admin_events(
  tenant_id,actor_member_id,subject_member_id,event_type,evidence)
SELECT tenant_id,{quote(actor_id)}::uuid,member_id,'role_revoked',
       jsonb_build_object('role','platform_admin','acceptance_batch',{quote(batch)})
FROM removed;

WITH removed AS (
  DELETE FROM capability.member_grants
  WHERE tenant_id={quote(TENANT_ID)}::uuid
    AND member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
    AND permission='knowledge:read'
  RETURNING tenant_id,member_id,permission
)
INSERT INTO audit.capability_admin_events(
  tenant_id,actor_member_id,subject_member_id,event_type,evidence)
SELECT tenant_id,{quote(actor_id)}::uuid,member_id,'member_grant_removed',
       jsonb_build_object('permission',permission,'acceptance_batch',{quote(batch)})
FROM removed;

DELETE FROM identity.member_devices
WHERE member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
  AND device_id={quote(DEVICE_ID)} AND platform_id={PLATFORM_ID};

UPDATE identity.members
SET status='disabled',updated_at=now()
WHERE id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
  AND tenant_id={quote(TENANT_ID)}::uuid;
COMMIT;
"""
    run_psql(sql)
    residual = run_psql(
        f"""
SELECT
  (SELECT count(*) FROM identity.members
    WHERE id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)
      AND status='disabled'),
  (SELECT count(*) FROM identity.member_devices
    WHERE member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)),
  (SELECT count(*) FROM identity.member_roles
    WHERE member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid)),
  (SELECT count(*) FROM capability.member_grants
    WHERE member_id IN ({quote(a["member_id"])}::uuid,{quote(b["member_id"])}::uuid));
"""
    )
    if residual != "2|0|0|0":
        raise ContractError(f"identity disable left residual authorization state: {residual}")


def systemctl(*arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        ["systemctl", *arguments],
        text=True,
        capture_output=True,
        check=False,
    )
    if check and completed.returncode != 0:
        raise ContractError(
            f"systemd operation {' '.join(arguments)} failed for the acceptance boundary"
        )
    return completed


def assert_no_acceptance_memory_jobs(state: dict[str, Any]) -> None:
    a = state["members"]["authorized"]["member_id"]
    b = state["members"]["denied"]["member_id"]
    row = run_psql(
        f"""
SELECT
  (SELECT count(*) FROM memory.extraction_jobs
    WHERE member_id IN ({quote(a)}::uuid,{quote(b)}::uuid)),
  (SELECT count(*) FROM memory.streams
    WHERE owner_member_id IN ({quote(a)}::uuid,{quote(b)}::uuid));
"""
    )
    if row != "0|0":
        raise ContractError(f"acceptance identities already own Memory state: {row}")


def command_isolate_begin(args: argparse.Namespace) -> None:
    if os.name == "nt" or os.geteuid() != 0:
        raise ContractError("isolate-begin must run as root on Node2")
    state_path = Path(args.state).resolve()
    state = validate_state(read_json(state_path))
    if state["status"] != "prepared":
        raise ContractError("enterprise RAG identities must be prepared before isolation")
    isolation = state.get("runtime_isolation")
    if isinstance(isolation, dict) and isolation.get("state") == "active":
        if systemctl("is-active", "--quiet", MEMORY_EXTRACTOR_UNIT, check=False).returncode == 0:
            raise ContractError("Memory extractor restarted during active acceptance isolation")
        assert_no_acceptance_memory_jobs(state)
        print(f"enterprise_rag_runtime_isolation=active batch_id={state['batch_id']}")
        return
    if isolation is not None and (
        not isinstance(isolation, dict) or isolation.get("state") not in {"prepared", "restored"}
    ):
        raise ContractError("enterprise RAG runtime isolation state is malformed")
    assert_no_acceptance_memory_jobs(state)
    if systemctl("is-active", "--quiet", MEMORY_EXTRACTOR_UNIT, check=False).returncode != 0:
        if not isinstance(isolation, dict) or isolation.get("state") != "prepared":
            raise ContractError("Memory extractor is unexpectedly inactive before isolation")
    else:
        state["runtime_isolation"] = {"state": "prepared", "unit": MEMORY_EXTRACTOR_UNIT}
        write_private_json(state_path, state)
        systemctl("stop", MEMORY_EXTRACTOR_UNIT)
    if systemctl("is-active", "--quiet", MEMORY_EXTRACTOR_UNIT, check=False).returncode == 0:
        raise ContractError("Memory extractor did not stop for acceptance isolation")
    state = validate_state(read_json(state_path))
    state["runtime_isolation"] = {"state": "active", "unit": MEMORY_EXTRACTOR_UNIT}
    write_private_json(state_path, state)
    print(f"enterprise_rag_runtime_isolation=active batch_id={state['batch_id']}")


def command_isolate_end(args: argparse.Namespace) -> None:
    if os.name == "nt" or os.geteuid() != 0:
        raise ContractError("isolate-end must run as root on Node2")
    state_path = Path(args.state).resolve()
    state = validate_state(read_json(state_path))
    isolation = state.get("runtime_isolation")
    if not isinstance(isolation, dict) or isolation.get("unit") != MEMORY_EXTRACTOR_UNIT:
        raise ContractError("enterprise RAG runtime isolation was not prepared")
    if isolation.get("state") == "restored":
        systemctl("is-active", "--quiet", MEMORY_EXTRACTOR_UNIT)
        print(f"enterprise_rag_runtime_isolation=restored batch_id={state['batch_id']}")
        return
    if isolation.get("state") not in {"prepared", "active"}:
        raise ContractError("enterprise RAG runtime isolation state is malformed")
    assert_no_acceptance_memory_jobs(state)
    systemctl("start", MEMORY_EXTRACTOR_UNIT)
    systemctl("is-active", "--quiet", MEMORY_EXTRACTOR_UNIT)
    state["runtime_isolation"] = {"state": "restored", "unit": MEMORY_EXTRACTOR_UNIT}
    write_private_json(state_path, state)
    print(f"enterprise_rag_runtime_isolation=restored batch_id={state['batch_id']}")


def validate_state(value: Any) -> dict[str, Any]:
    validate_contract(value, require_password=False)
    if value.get("status") not in {"prepared", "disabled"}:
        raise ContractError("enterprise RAG identity state status is invalid")
    actor = value.get("actor_member_id")
    if actor != BASELINE_ACTOR_ID:
        raise ContractError("enterprise RAG identity state actor is not authoritative")
    return value


def command_contract(args: argparse.Namespace) -> None:
    path = Path(args.output).resolve()
    if path.exists():
        contract = validate_contract(read_json(path), require_password=True)
        if contract["batch_id"] != args.batch_id:
            raise ContractError("existing identity credential file belongs to another batch")
        print(f"enterprise_rag_identity_contract=existing batch_id={args.batch_id}")
        return
    contract = build_contract(args.batch_id)
    write_private_json(path, contract)
    print(f"enterprise_rag_identity_contract=created batch_id={args.batch_id}")


def command_prepare(args: argparse.Namespace) -> None:
    if os.name == "nt" or os.geteuid() != 0:
        raise ContractError("prepare must run as root on Node2")
    credential_path = Path(args.credentials).resolve()
    state_path = Path(args.state).resolve()
    require_private_file(credential_path)
    contract = validate_contract(read_json(credential_path), require_password=True)
    env = load_env(Path(args.platform_env))
    issuer = required_env(env, "PLATFORM_OIDC_ISSUER")
    actor_id = find_fixture_actor()
    preflight_prepare(contract)
    token = keycloak_admin_token(required_env(env, "PLATFORM_LOCAL_KEYCLOAK_ADMIN_PASSWORD"))
    for role in ("authorized", "denied"):
        ensure_keycloak_user(token, contract["members"][role])
    prepare_database(contract, issuer, actor_id)
    state = sanitize_contract(contract, "prepared", actor_id)
    verify_database(state, require_openim=False)
    write_private_json(state_path, state)
    print(f"enterprise_rag_identities=prepared batch_id={contract['batch_id']}")
    print(f"authorized_member_id={state['members']['authorized']['member_id']}")
    print(f"denied_member_id={state['members']['denied']['member_id']}")


def command_verify(args: argparse.Namespace) -> None:
    state = validate_state(read_json(Path(args.state).resolve()))
    if state["status"] != "prepared":
        raise ContractError("enterprise RAG identities are not in prepared state")
    verify_database(state, require_openim=args.require_openim)
    print(
        "enterprise_rag_identities=verified"
        f" openim={'ready' if args.require_openim else 'not_required'}"
    )


def command_disable(args: argparse.Namespace) -> None:
    if os.name == "nt" or os.geteuid() != 0:
        raise ContractError("disable must run as root on Node2")
    state_path = Path(args.state).resolve()
    state = validate_state(read_json(state_path))
    if state["status"] == "disabled":
        print(f"enterprise_rag_identities=disabled batch_id={state['batch_id']}")
        return
    verify_database(state, require_openim=True)
    preflight_disable(state)
    env = load_env(Path(args.platform_env))
    token = keycloak_admin_token(required_env(env, "PLATFORM_LOCAL_KEYCLOAK_ADMIN_PASSWORD"))
    for role in ("authorized", "denied"):
        disable_keycloak_user(token, state["members"][role])
    clear_isolated_openim_messages(state, env)
    disable_database(state)
    state["status"] = "disabled"
    write_private_json(state_path, state)
    print(f"enterprise_rag_identities=disabled batch_id={state['batch_id']}")


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser(
        description="Manage isolated Node2 enterprise RAG acceptance identities."
    )
    commands = value.add_subparsers(dest="command", required=True)

    create = commands.add_parser("contract")
    create.add_argument("batch_id")
    create.add_argument("output")
    create.set_defaults(handler=command_contract)

    prepare = commands.add_parser("prepare")
    prepare.add_argument("credentials")
    prepare.add_argument("state")
    prepare.add_argument("--platform-env", default=PLATFORM_ENV)
    prepare.set_defaults(handler=command_prepare)

    verify = commands.add_parser("verify")
    verify.add_argument("state")
    verify.add_argument("--require-openim", action="store_true")
    verify.set_defaults(handler=command_verify)

    disable = commands.add_parser("disable")
    disable.add_argument("state")
    disable.add_argument("--platform-env", default=PLATFORM_ENV)
    disable.set_defaults(handler=command_disable)

    isolate_begin = commands.add_parser("isolate-begin")
    isolate_begin.add_argument("state")
    isolate_begin.set_defaults(handler=command_isolate_begin)

    isolate_end = commands.add_parser("isolate-end")
    isolate_end.add_argument("state")
    isolate_end.set_defaults(handler=command_isolate_end)
    return value


def main() -> int:
    args = parser().parse_args()
    try:
        args.handler(args)
    except ContractError as exc:
        print(f"enterprise RAG identity operation failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
