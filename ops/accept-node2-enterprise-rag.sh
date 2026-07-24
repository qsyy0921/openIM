#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
shift || true

postgres_container="${OPENIM_PLATFORM_POSTGRES_CONTAINER:-openim-platform-local-postgres-1}"
ingestion_unit="${OPENIM_KNOWLEDGE_INGESTION_UNIT:-openim-knowledge-ingestion.service}"

usage() {
  cat >&2 <<'EOF'
usage:
  accept-node2-enterprise-rag.sh contract STATE_JSON MANIFEST_JSON
  accept-node2-enterprise-rag.sh verify-web STATE_JSON MANIFEST_JSON TENANT_ID AUTHORIZED_MEMBER_ID DENIED_MEMBER_ID
  accept-node2-enterprise-rag.sh cleanup STATE_JSON MANIFEST_JSON TENANT_ID AUTHORIZED_MEMBER_ID DENIED_MEMBER_ID CONFIRM_BATCH_ID
EOF
  exit 2
}

valid_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]
}

valid_batch_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9-]{7,63}$ ]]
}

psql_value() {
  docker exec "$postgres_container" psql -At -F '|' -v ON_ERROR_STOP=1 \
    -U platform -d platform -c "$1"
}

declare batch_id=""
declare invalid_document_id=""
declare invalid_version_id=""
declare -A document_id=()
declare -A initial_version_id=()
declare -A next_version_id=()
declare -A marker=()
declare -A next_marker=()
declare -A filename=()
declare -A next_filename=()
declare -A web_run_id=()

load_contract() {
  local state_path="$1"
  local manifest_path="$2"
  [[ -f "$state_path" && -f "$manifest_path" ]] || {
    echo "acceptance state and manifest must be regular files" >&2
    exit 1
  }

  local contract_file
  contract_file="$(mktemp)"
  if ! python3 - "$state_path" "$manifest_path" >"$contract_file" <<'PY'
import json
import re
import sys
import uuid
from pathlib import Path

state_path, manifest_path = map(Path, sys.argv[1:])
state = json.loads(state_path.read_text(encoding="utf-8"))
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

batch_pattern = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]{7,63}$")
marker_pattern = re.compile(r"^[A-Za-z0-9-]{8,96}$")
expected_files = {
    "markdown": ("policy-markdown.md", "policy-markdown-v2.md"),
    "text": ("policy-text.txt", ""),
    "pdf": ("policy-pdf.pdf", ""),
    "docx": ("policy-docx.docx", ""),
}

def fail(message: str) -> None:
    raise SystemExit(message)

def uuid_text(value: object, label: str) -> str:
    if not isinstance(value, str):
        fail(f"{label} must be a UUID")
    try:
        parsed = uuid.UUID(value)
    except ValueError:
        fail(f"{label} must be a UUID")
    if parsed.version not in {1, 2, 3, 4, 5}:
        fail(f"{label} uses an unsupported UUID version")
    return str(parsed)

def scalar(value: object, label: str, pattern: re.Pattern[str] | None = None) -> str:
    if not isinstance(value, str) or not value or any(char in value for char in "|\r\n"):
        fail(f"{label} is malformed")
    if pattern and not pattern.fullmatch(value):
        fail(f"{label} is malformed")
    return value

if manifest.get("schema_version") != 1 or state.get("schema_version") != 1:
    fail("unsupported enterprise RAG acceptance schema")
batch_id = scalar(manifest.get("batch_id"), "manifest batch ID", batch_pattern)
if state.get("batch_id") != batch_id:
    fail("acceptance state belongs to another batch")
manifest_documents = manifest.get("documents")
state_documents = state.get("documents")
if not isinstance(manifest_documents, list) or len(manifest_documents) != 4:
    fail("manifest must contain exactly four documents")
if not isinstance(state_documents, list) or len(state_documents) != 4:
    fail("state must contain exactly four documents")

manifest_by_format: dict[str, dict[str, object]] = {}
for item in manifest_documents:
    if not isinstance(item, dict):
        fail("manifest document is malformed")
    fmt = scalar(item.get("format"), "manifest format")
    if fmt not in expected_files or fmt in manifest_by_format:
        fail("manifest formats are missing or duplicated")
    expected_file, expected_next = expected_files[fmt]
    if item.get("filename") != expected_file or (item.get("version_file") or "") != expected_next:
        fail(f"{fmt} fixture filenames do not match the locked contract")
    expected_title = {
        "markdown": "E2E RAG Markdown ",
        "text": "E2E RAG TXT ",
        "pdf": "E2E RAG PDF ",
        "docx": "E2E RAG DOCX ",
    }[fmt] + batch_id
    if item.get("title") != expected_title:
        fail(f"{fmt} title does not match the isolated batch")
    scalar(item.get("marker"), f"{fmt} marker", marker_pattern)
    if fmt == "markdown":
        scalar(item.get("version_marker"), "Markdown version marker", marker_pattern)
    elif item.get("version_marker") not in (None, ""):
        fail(f"{fmt} must not define a second-version marker")
    manifest_by_format[fmt] = item

state_by_format: dict[str, dict[str, object]] = {}
for item in state_documents:
    if not isinstance(item, dict):
        fail("state document is malformed")
    fmt = scalar(item.get("format"), "state format")
    if fmt not in manifest_by_format or fmt in state_by_format:
        fail("state formats are missing or duplicated")
    manifest_item = manifest_by_format[fmt]
    for key in ("title", "filename", "marker"):
        if item.get(key) != manifest_item.get(key):
            fail(f"{fmt} state does not match manifest field {key}")
    if (item.get("version_file") or "") != (manifest_item.get("version_file") or "") or \
       (item.get("version_marker") or "") != (manifest_item.get("version_marker") or ""):
        fail(f"{fmt} version fixture does not match the manifest")
    uuid_text(item.get("document_id"), f"{fmt} document ID")
    uuid_text(item.get("version_id"), f"{fmt} initial version ID")
    if item.get("version_number") != 1:
        fail(f"{fmt} initial version number must be 1")
    if fmt == "markdown":
        uuid_text(item.get("next_version_id"), "Markdown next version ID")
        if item.get("next_version_number") != 2:
            fail("Markdown next version number must be 2")
    elif item.get("next_version_id") is not None or item.get("next_version_number") is not None:
        fail(f"{fmt} unexpectedly contains another version")
    state_by_format[fmt] = item

invalid = state.get("invalid_document")
if not isinstance(invalid, dict) or invalid.get("title") != f"E2E RAG Invalid PDF {batch_id}":
    fail("invalid PDF state is missing or belongs to another batch")
invalid_document_id = uuid_text(invalid.get("document_id"), "invalid PDF document ID")
invalid_version_id = uuid_text(invalid.get("version_id"), "invalid PDF version ID")

web = state.get("web")
if not isinstance(web, dict):
    fail("Web run state is missing")
run_keys = ("bootstrap_run_id", "denied_run_id", "revoked_run_id", "version_run_id")
web_runs = {key: uuid_text(web.get(key), f"Web {key}") for key in run_keys}

print(f"batch|{batch_id}")
for fmt in ("markdown", "text", "pdf", "docx"):
    item = state_by_format[fmt]
    print("|".join([
        "document", fmt,
        uuid_text(item["document_id"], f"{fmt} document ID"),
        uuid_text(item["version_id"], f"{fmt} initial version ID"),
        uuid_text(item["next_version_id"], "Markdown next version ID") if fmt == "markdown" else "",
        scalar(item["marker"], f"{fmt} marker", marker_pattern),
        scalar(item["version_marker"], "Markdown version marker", marker_pattern) if fmt == "markdown" else "",
        scalar(item["filename"], f"{fmt} filename"),
        scalar(item["version_file"], "Markdown next filename") if fmt == "markdown" else "",
    ]))
print(f"invalid|{invalid_document_id}|{invalid_version_id}")
for key in run_keys:
    print(f"web-run|{key}|{web_runs[key]}")
PY
  then
    rm -f -- "$contract_file"
    exit 1
  fi

  while IFS='|' read -r kind value1 value2 value3 value4 value5 value6 value7 value8; do
    case "$kind" in
      batch)
        batch_id="$value1"
        ;;
      document)
        document_id["$value1"]="$value2"
        initial_version_id["$value1"]="$value3"
        next_version_id["$value1"]="$value4"
        marker["$value1"]="$value5"
        next_marker["$value1"]="$value6"
        filename["$value1"]="$value7"
        next_filename["$value1"]="$value8"
        ;;
      invalid)
        invalid_document_id="$value1"
        invalid_version_id="$value2"
        ;;
      web-run)
        web_run_id["$value1"]="$value2"
        ;;
      *)
        rm -f -- "$contract_file"
        echo "acceptance contract parser returned an unknown record" >&2
        exit 1
        ;;
    esac
  done <"$contract_file"
  rm -f -- "$contract_file"

  valid_batch_id "$batch_id" || {
    echo "parsed batch ID is invalid" >&2
    exit 1
  }
  for value in "$invalid_document_id" "$invalid_version_id" \
    "${document_id[markdown]}" "${document_id[text]}" "${document_id[pdf]}" "${document_id[docx]}" \
    "${initial_version_id[markdown]}" "${initial_version_id[text]}" \
    "${initial_version_id[pdf]}" "${initial_version_id[docx]}" \
    "${next_version_id[markdown]}" \
    "${web_run_id[bootstrap_run_id]}" "${web_run_id[denied_run_id]}" \
    "${web_run_id[revoked_run_id]}" "${web_run_id[version_run_id]}"; do
    valid_uuid "$value" || {
      echo "parsed acceptance UUID is invalid" >&2
      exit 1
    }
  done
  echo "enterprise_rag_contract=valid batch_id=$batch_id"
}

document_ids_sql() {
  printf "'%s'::uuid,'%s'::uuid,'%s'::uuid,'%s'::uuid,'%s'::uuid" \
    "${document_id[markdown]}" "${document_id[text]}" "${document_id[pdf]}" \
    "${document_id[docx]}" "$invalid_document_id"
}

target_document_ids_sql() {
  printf "'%s'::uuid,'%s'::uuid,'%s'::uuid,'%s'::uuid" \
    "${document_id[markdown]}" "${document_id[text]}" "${document_id[pdf]}" "${document_id[docx]}"
}

assert_terminal_run() {
  local run_id="$1"
  local expected_member_id="$2"
  local require_model="$3"
  local row
  row="$(psql_value "
SELECT state,source_channel,principal_member_id::text,COALESCE(model,''),
       (provider_response_id IS NOT NULL)::text
FROM agent.runs
WHERE id='$run_id'::uuid")"
  IFS='|' read -r state source_channel principal_member_id model has_provider <<<"$row"
  [[ "$state" == succeeded && "$source_channel" == openim && \
     "$principal_member_id" == "$expected_member_id" ]] || {
    echo "Web Agent Run $run_id does not match its terminal identity contract" >&2
    exit 1
  }
  if [[ "$require_model" == true ]]; then
    [[ "$model" == gpt-5.6-terra && "$has_provider" == true ]] || {
      echo "Web Agent Run $run_id did not use the locked Terra generation contract" >&2
      exit 1
    }
  fi
}

verify_successful_version() {
  local tenant_id="$1"
  local format="$2"
  local version_id="$3"
  local expected_status="$4"
  local expected_filename="$5"
  local row
  row="$(psql_value "
SELECT version.ingestion_state,version.status,source.state,source.original_filename,
       job.state,job.chunk_count,job.vector_count,
       (SELECT count(*) FROM knowledge.chunks chunk WHERE chunk.version_id=version.id),
       (SELECT count(*)
          FROM knowledge.chunk_search_indexes search_index
          JOIN knowledge.chunks chunk ON chunk.id=search_index.chunk_id
          JOIN knowledge.index_generations generation
            ON generation.id=search_index.generation_id
           AND generation.tenant_id=search_index.tenant_id
         WHERE chunk.version_id=version.id
           AND generation.state='active'
           AND generation.projection_revision='document-title-content-v1'
           AND search_index.projection_revision=generation.projection_revision)
FROM knowledge.document_versions version
JOIN knowledge.document_source_objects source ON source.version_id=version.id
JOIN knowledge.ingestion_jobs job ON job.version_id=version.id
WHERE version.tenant_id='$tenant_id'::uuid
  AND version.id='$version_id'::uuid")"
  local ingestion_state version_status object_state original_filename job_state
  local job_chunks job_vectors chunk_count search_count
  IFS='|' read -r ingestion_state version_status object_state original_filename job_state \
    job_chunks job_vectors chunk_count search_count <<<"$row"
  [[ "$ingestion_state" == indexed && "$version_status" == "$expected_status" && \
     "$object_state" == stored && "$original_filename" == "$expected_filename" && \
     "$job_state" == succeeded && "$job_chunks" -gt 0 && \
     "$job_chunks" -eq "$job_vectors" && "$job_chunks" -eq "$chunk_count" && \
     "$chunk_count" -eq "$search_count" ]] || {
    echo "$format version $version_id does not match the indexed projection contract: $row" >&2
    exit 1
  }
}

verify_web() {
  local tenant_id="$1"
  local authorized_member_id="$2"
  local denied_member_id="$3"
  valid_uuid "$tenant_id" && valid_uuid "$authorized_member_id" && valid_uuid "$denied_member_id" || usage
  docker inspect "$postgres_container" >/dev/null

  local all_document_ids target_document_ids
  all_document_ids="$(document_ids_sql)"
  target_document_ids="$(target_document_ids_sql)"

  local document_count
  document_count="$(psql_value "
SELECT count(*)
FROM knowledge.documents
WHERE tenant_id='$tenant_id'::uuid
  AND id IN ($all_document_ids)
  AND classification='internal'
  AND status='active'
  AND source_uri='knowledge://' || id::text
  AND title LIKE 'E2E RAG % $batch_id'")"
  [[ "$document_count" -eq 5 ]] || {
    echo "isolated enterprise RAG documents are missing or changed" >&2
    exit 1
  }

  local current_version
  for format in markdown text pdf docx; do
    current_version="${initial_version_id[$format]}"
    [[ "$format" == markdown ]] && current_version="${next_version_id[markdown]}"
    local expected_title="E2E RAG ${format^^} $batch_id"
    case "$format" in
      markdown) expected_title="E2E RAG Markdown $batch_id" ;;
      text) expected_title="E2E RAG TXT $batch_id" ;;
      pdf) expected_title="E2E RAG PDF $batch_id" ;;
      docx) expected_title="E2E RAG DOCX $batch_id" ;;
    esac
    [[ "$(psql_value "
SELECT count(*)
FROM knowledge.documents
WHERE tenant_id='$tenant_id'::uuid
  AND id='${document_id[$format]}'::uuid
  AND title='$expected_title'
  AND current_version_id='$current_version'::uuid")" -eq 1 ]] || {
      echo "$format current version does not match the acceptance state" >&2
      exit 1
    }
  done

  verify_successful_version "$tenant_id" markdown "${initial_version_id[markdown]}" superseded "${filename[markdown]}"
  verify_successful_version "$tenant_id" markdown "${next_version_id[markdown]}" published "${next_filename[markdown]}"
  verify_successful_version "$tenant_id" text "${initial_version_id[text]}" published "${filename[text]}"
  verify_successful_version "$tenant_id" pdf "${initial_version_id[pdf]}" published "${filename[pdf]}"
  verify_successful_version "$tenant_id" docx "${initial_version_id[docx]}" published "${filename[docx]}"

  local invalid_row=""
  for _ in $(seq 1 60); do
    invalid_row="$(psql_value "
SELECT version.ingestion_state,version.ingestion_failure_code,source.state,job.state,job.failure_code
FROM knowledge.document_versions version
JOIN knowledge.document_source_objects source ON source.version_id=version.id
JOIN knowledge.ingestion_jobs job ON job.version_id=version.id
WHERE version.tenant_id='$tenant_id'::uuid
  AND version.document_id='$invalid_document_id'::uuid
  AND version.id='$invalid_version_id'::uuid")"
    [[ "$invalid_row" == "failed|PDF_STRUCTURE_INVALID|deleted|failed|PDF_STRUCTURE_INVALID" ]] && break
    [[ "$invalid_row" == *"|delete_failed|"* ]] && break
    sleep 2
  done
  [[ "$invalid_row" == "failed|PDF_STRUCTURE_INVALID|deleted|failed|PDF_STRUCTURE_INVALID" ]] || {
    echo "invalid PDF did not fail closed and clean its exact source object: $invalid_row" >&2
    exit 1
  }

  local grant_row
  grant_row="$(psql_value "
SELECT
  count(*) FILTER (WHERE member_id='$authorized_member_id'::uuid
                    AND document_id='${document_id[markdown]}'::uuid),
  count(*) FILTER (WHERE member_id='$authorized_member_id'::uuid
                    AND document_id<>'${document_id[markdown]}'::uuid),
  count(*) FILTER (WHERE member_id='$denied_member_id'::uuid)
FROM authz.document_grants
WHERE tenant_id='$tenant_id'::uuid
  AND document_id IN ($target_document_ids)")"
  [[ "$grant_row" == "1|0|0" ]] || {
    echo "final Web grant state is not Markdown-only for member A and denied for member B: $grant_row" >&2
    exit 1
  }

  local bootstrap_run="${web_run_id[bootstrap_run_id]}"
  local denied_run="${web_run_id[denied_run_id]}"
  local revoked_run="${web_run_id[revoked_run_id]}"
  local version_run="${web_run_id[version_run_id]}"
  assert_terminal_run "$bootstrap_run" "$authorized_member_id" true
  assert_terminal_run "$denied_run" "$denied_member_id" false
  assert_terminal_run "$revoked_run" "$authorized_member_id" false
  assert_terminal_run "$version_run" "$authorized_member_id" true

  local bootstrap_citations
  bootstrap_citations="$(psql_value "
SELECT count(*),count(DISTINCT citation.document_id),
       count(*) FILTER (
         WHERE citation.authorized_excerpt IS NULL
            OR citation.checksum<>chunk.checksum),
       count(*) FILTER (
         WHERE citation.version_id NOT IN (
           '${initial_version_id[markdown]}'::uuid,
           '${initial_version_id[text]}'::uuid,
           '${initial_version_id[pdf]}'::uuid,
           '${initial_version_id[docx]}'::uuid))
FROM agent.run_citations citation
JOIN knowledge.chunks chunk ON chunk.id=citation.chunk_id
WHERE citation.run_id='$bootstrap_run'::uuid
  AND citation.document_id IN ($target_document_ids)")"
  local bootstrap_total bootstrap_documents bootstrap_invalid bootstrap_wrong_version
  IFS='|' read -r bootstrap_total bootstrap_documents bootstrap_invalid bootstrap_wrong_version \
    <<<"$bootstrap_citations"
  [[ "$bootstrap_total" -ge 4 && "$bootstrap_documents" -eq 4 && \
     "$bootstrap_invalid" -eq 0 && "$bootstrap_wrong_version" -eq 0 ]] || {
    echo "Web bootstrap citations do not cover four checksum-valid initial versions: $bootstrap_citations" >&2
    exit 1
  }
  for format in markdown text pdf docx; do
    [[ "$(psql_value "
SELECT (position('${marker[$format]}' in COALESCE(candidate_text,''))>0)::text
FROM agent.runs WHERE id='$bootstrap_run'::uuid")" == true ]] || {
      echo "Web bootstrap answer omitted the $format control marker" >&2
      exit 1
    }
  done

  local denied_leakage revoked_leakage
  denied_leakage="$(psql_value "
SELECT
  (SELECT count(*) FROM agent.run_citations
    WHERE run_id='$denied_run'::uuid AND document_id IN ($target_document_ids)),
  (SELECT count(*)
     FROM agent.tool_calls call
     CROSS JOIN LATERAL jsonb_array_elements(COALESCE(call.result,'[]'::jsonb)) item
    WHERE call.run_id='$denied_run'::uuid
      AND item->>'document_id' IN (
        '${document_id[markdown]}','${document_id[text]}',
        '${document_id[pdf]}','${document_id[docx]}'))")"
  revoked_leakage="$(psql_value "
SELECT
  (SELECT count(*) FROM agent.run_citations
    WHERE run_id='$revoked_run'::uuid AND document_id IN ($target_document_ids)),
  (SELECT count(*)
     FROM agent.tool_calls call
     CROSS JOIN LATERAL jsonb_array_elements(COALESCE(call.result,'[]'::jsonb)) item
    WHERE call.run_id='$revoked_run'::uuid
      AND item->>'document_id' IN (
        '${document_id[markdown]}','${document_id[text]}',
        '${document_id[pdf]}','${document_id[docx]}'))")"
  [[ "$denied_leakage" == "0|0" && "$revoked_leakage" == "0|0" ]] || {
    echo "unauthorized Web evidence escaped ACL-first retrieval: denied=$denied_leakage revoked=$revoked_leakage" >&2
    exit 1
  }

  local version_evidence
  version_evidence="$(psql_value "
SELECT
  count(*) FILTER (WHERE version_id='${next_version_id[markdown]}'::uuid),
  count(*) FILTER (WHERE version_id='${initial_version_id[markdown]}'::uuid),
  (position('${next_marker[markdown]}' in COALESCE(run.candidate_text,''))>0)::text,
  (position('${marker[markdown]}' in COALESCE(run.candidate_text,''))>0)::text
FROM agent.runs run
LEFT JOIN agent.run_citations citation ON citation.run_id=run.id
WHERE run.id='$version_run'::uuid
GROUP BY run.candidate_text")"
  [[ "$version_evidence" =~ ^[1-9][0-9]*\|0\|true\|false$ ]] || {
    echo "new-version Web Run retained old evidence or omitted the new marker: $version_evidence" >&2
    exit 1
  }

  local foreign_citations
  foreign_citations="$(psql_value "
SELECT count(*)
FROM agent.run_citations citation
JOIN agent.runs run ON run.id=citation.run_id
WHERE citation.document_id IN ($target_document_ids)
  AND position('$batch_id' in run.prompt)=0")"
  [[ "$foreign_citations" -eq 0 ]] || {
    echo "acceptance documents were cited outside the isolated batch" >&2
    exit 1
  }

  echo "enterprise_rag_web=accepted batch_id=$batch_id bootstrap_run_id=$bootstrap_run version_run_id=$version_run"
  echo "enterprise_rag_acl=denied_member_zero|revoked_member_zero"
  echo "enterprise_rag_version=old_zero|new_current"
}

cleanup_fixture() {
  local tenant_id="$1"
  local authorized_member_id="$2"
  local denied_member_id="$3"
  local confirmation="$4"
  valid_uuid "$tenant_id" && valid_uuid "$authorized_member_id" && valid_uuid "$denied_member_id" || usage
  [[ "$confirmation" == "$batch_id" ]] || {
    echo "cleanup confirmation must exactly match the isolated batch ID" >&2
    exit 1
  }
  docker inspect "$postgres_container" >/dev/null
  systemctl is-active --quiet "$ingestion_unit"

  local all_document_ids target_document_ids
  all_document_ids="$(document_ids_sql)"
  target_document_ids="$(target_document_ids_sql)"
  local ready_state
  ready_state="$(psql_value "
SELECT
  count(*) FILTER (WHERE current_version_id IS NOT NULL),
  (SELECT count(*) FROM authz.document_grants
    WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($target_document_ids)),
  count(*)
FROM knowledge.documents
WHERE tenant_id='$tenant_id'::uuid AND id IN ($all_document_ids)")"
  [[ "$ready_state" == "0|0|5" ]] || {
    echo "cleanup requires five exact documents, no publication pointer, and no remaining grant: $ready_state" >&2
    exit 1
  }

  local unknown_reference_count
  unknown_reference_count="$(psql_value "
WITH fixture_runs AS (
  SELECT DISTINCT run.id
  FROM agent.runs run
  LEFT JOIN agent.run_citations citation ON citation.run_id=run.id
  WHERE run.tenant_id='$tenant_id'::uuid
    AND (
      position('$batch_id' in run.prompt)>0
      OR citation.document_id IN ($target_document_ids)
    )
)
SELECT
  (SELECT count(*) FROM memory.events event
    WHERE event.source_run_id IN (SELECT id FROM fixture_runs))
  + (SELECT count(*) FROM memory.facts fact
    WHERE fact.source_run_id IN (SELECT id FROM fixture_runs))
  + (SELECT count(*) FROM agent.delegations delegation
    WHERE delegation.parent_run_id IN (SELECT id FROM fixture_runs)
       OR delegation.child_run_id IN (SELECT id FROM fixture_runs))
  + (SELECT count(*) FROM agent.remote_a2a_jobs job
    WHERE job.parent_run_id IN (SELECT id FROM fixture_runs))
  + (SELECT count(*) FROM proactive.source_events event
    WHERE event.run_id IN (SELECT id FROM fixture_runs))")"
  [[ "$unknown_reference_count" -eq 0 ]] || {
    echo "fixture Runs have non-cleanable domain references; cleanup stopped: $unknown_reference_count" >&2
    exit 1
  }

  local run_contract
  run_contract="$(psql_value "
WITH fixture_runs AS (
  SELECT DISTINCT run.id,run.state,run.principal_member_id,run.source_channel
  FROM agent.runs run
  LEFT JOIN agent.run_citations citation ON citation.run_id=run.id
  WHERE run.tenant_id='$tenant_id'::uuid
    AND (
      position('$batch_id' in run.prompt)>0
      OR citation.document_id IN ($target_document_ids)
    )
)
SELECT
  count(*),
  count(*) FILTER (WHERE state NOT IN ('succeeded','failed')),
  count(*) FILTER (
    WHERE principal_member_id NOT IN (
      '$authorized_member_id'::uuid,'$denied_member_id'::uuid)),
  count(*) FILTER (WHERE source_channel NOT IN ('openim','telegram'))
FROM fixture_runs")"
  local run_count nonterminal_run_count foreign_member_count invalid_channel_count
  IFS='|' read -r run_count nonterminal_run_count foreign_member_count invalid_channel_count <<<"$run_contract"
  [[ "$run_count" -ge 4 && "$nonterminal_run_count" -eq 0 && \
     "$foreign_member_count" -eq 0 && "$invalid_channel_count" -eq 0 ]] || {
    echo "fixture Run ownership or terminal-state contract failed: $run_contract" >&2
    exit 1
  }

  local source_contract
  source_contract="$(psql_value "
SELECT
  count(*),
  count(*) FILTER (WHERE state NOT IN ('stored','deleted')),
  count(*) FILTER (
    WHERE object_key<>tenant_id::text || '/' || document_id::text || '/' || version_id::text || '/source')
FROM knowledge.document_source_objects
WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids)")"
  local source_count invalid_source_state invalid_object_key
  IFS='|' read -r source_count invalid_source_state invalid_object_key <<<"$source_contract"
  [[ "$source_count" -eq 6 && "$invalid_source_state" -eq 0 && "$invalid_object_key" -eq 0 ]] || {
    echo "source objects do not belong exclusively to the isolated fixture: $source_contract" >&2
    exit 1
  }

  psql_value "
BEGIN;
UPDATE knowledge.document_source_objects
SET state='delete_pending',
    cleanup_available_at=now(),
    cleanup_lease_owner=NULL,
    cleanup_lease_token=NULL,
    cleanup_lease_expires_at=NULL,
    cleanup_failure_detail=NULL,
    updated_at=now()
WHERE tenant_id='$tenant_id'::uuid
  AND document_id IN ($all_document_ids)
  AND state='stored';
INSERT INTO audit.knowledge_events
  (tenant_id,actor_member_id,document_id,version_id,event_type,evidence)
SELECT tenant_id,'$authorized_member_id'::uuid,document_id,version_id,
       'e2e_cleanup_scheduled',
       jsonb_build_object('batch_id','$batch_id')
FROM knowledge.document_source_objects
WHERE tenant_id='$tenant_id'::uuid
  AND document_id IN ($all_document_ids)
  AND state='delete_pending';
COMMIT;" >/dev/null

  local object_states=""
  for _ in $(seq 1 120); do
    object_states="$(psql_value "
SELECT count(*),count(*) FILTER (WHERE state='deleted'),
       count(*) FILTER (WHERE state='delete_failed')
FROM knowledge.document_source_objects
WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids)")"
    local object_total object_deleted object_failed
    IFS='|' read -r object_total object_deleted object_failed <<<"$object_states"
    [[ "$object_total" -eq 6 && "$object_deleted" -eq 6 && "$object_failed" -eq 0 ]] && break
    [[ "$object_failed" -gt 0 ]] && {
      echo "knowledge source cleanup reached delete_failed: $object_states" >&2
      exit 1
    }
    sleep 2
  done
  [[ "$object_states" == "6|6|0" ]] || {
    echo "knowledge source cleanup did not delete every exact object: $object_states" >&2
    exit 1
  }

  psql_value "
BEGIN;
SET CONSTRAINTS ALL DEFERRED;
CREATE TEMP TABLE e2e_fixture_runs ON COMMIT DROP AS
SELECT DISTINCT run.id,run.source_event_id
FROM agent.runs run
LEFT JOIN agent.run_citations citation ON citation.run_id=run.id
WHERE run.tenant_id='$tenant_id'::uuid
  AND (
    position('$batch_id' in run.prompt)>0
    OR citation.document_id IN ($target_document_ids)
  );
DELETE FROM audit.delivery_events event
USING e2e_fixture_runs fixture
WHERE event.run_id=fixture.id;
DELETE FROM agent.runs run
USING e2e_fixture_runs fixture
WHERE run.id=fixture.id;
DELETE FROM integration.ingress_messages ingress
USING e2e_fixture_runs fixture
WHERE ingress.event_id=fixture.source_event_id;
DELETE FROM audit.knowledge_events
WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids);
UPDATE knowledge.documents
SET current_version_id=NULL
WHERE tenant_id='$tenant_id'::uuid AND id IN ($all_document_ids);
DELETE FROM knowledge.documents
WHERE tenant_id='$tenant_id'::uuid AND id IN ($all_document_ids);
COMMIT;" >/dev/null

  local residual
  residual="$(psql_value "
SELECT
  (SELECT count(*) FROM knowledge.documents
    WHERE tenant_id='$tenant_id'::uuid AND id IN ($all_document_ids)),
  (SELECT count(*) FROM knowledge.document_versions
    WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids)),
  (SELECT count(*) FROM knowledge.document_source_objects
    WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids)),
  (SELECT count(*) FROM knowledge.chunks
    WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids)),
  (SELECT count(*) FROM authz.document_grants
    WHERE tenant_id='$tenant_id'::uuid AND document_id IN ($all_document_ids)),
  (SELECT count(*) FROM agent.runs
    WHERE tenant_id='$tenant_id'::uuid AND position('$batch_id' in prompt)>0),
  (SELECT count(*) FROM agent.run_citations
    WHERE document_id IN ($target_document_ids))")"
  [[ "$residual" == "0|0|0|0|0|0|0" ]] || {
    echo "enterprise RAG fixture cleanup left residual PostgreSQL state: $residual" >&2
    exit 1
  }
  echo "enterprise_rag_source_objects=cleaned count=6"
  echo "enterprise_rag_platform_messages=cleaned runs=$run_count"
  echo "enterprise_rag_fixture=cleaned batch_id=$batch_id"
}

case "$mode" in
  contract)
    [[ "$#" -eq 2 ]] || usage
    load_contract "$1" "$2"
    ;;
  verify-web)
    [[ "$#" -eq 5 ]] || usage
    load_contract "$1" "$2"
    verify_web "$3" "$4" "$5"
    ;;
  cleanup)
    [[ "$#" -eq 6 ]] || usage
    load_contract "$1" "$2"
    cleanup_fixture "$3" "$4" "$5" "$6"
    ;;
  *)
    usage
    ;;
esac
