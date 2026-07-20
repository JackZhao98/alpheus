#!/usr/bin/env python3
"""Independent JSON Schema 2020-12 validation for Agent Platform contract packs."""

from __future__ import annotations

import json
import hashlib
import pathlib
import sys

from jsonschema import Draft202012Validator, RefResolver
from jsonschema.validators import validator_for


PROFILE = "alpheus-c14n-v1"
CONTRACT_TYPES = {
    ("alpheus.blob", "blob_ref.json"): "blob_ref",
    ("alpheus.blob", "lifecycle_event.json"): "blob_lifecycle_event",
    ("alpheus.blob", "reference.json"): "blob_reference",
    ("alpheus.blob", "stage_grant.json"): "blob_stage_grant",
    ("alpheus.blob", "staged_blob.json"): "blob_staged",
    ("alpheus.common", "command_envelope.json"): "command_envelope",
    ("alpheus.common", "effective_run_authority.json"): "effective_run_authority",
    ("alpheus.common", "run_origin_schedule.json"): "run_origin",
    ("alpheus.common", "run_origin_user.json"): "run_origin",
    ("alpheus.delivery", "inbox_receipt.json"): "inbox_receipt",
    ("alpheus.delivery", "outbox_available.json"): "outbox_record",
    ("alpheus.delivery", "quarantine_active.json"): "quarantine_record",
    ("alpheus.governance", "activation_receipt.json"): "activation_receipt",
    ("alpheus.governance", "effect_class_head.json"): "effect_class_head",
    ("alpheus.governance", "effect_class_revision.json"): "effect_class_revision",
    ("alpheus.governance", "governance_event.json"): "governance_event",
    ("alpheus.governance", "kill_switch_head.json"): "kill_switch_head",
    ("alpheus.governance", "kill_switch_revision.json"): "kill_switch_revision",
    ("alpheus.governance", "owner_policy_event.json"): "owner_policy_event",
    ("alpheus.governance", "owner_policy_head.json"): "owner_policy_head",
    ("alpheus.governance", "owner_policy_revision.json"): "owner_policy_revision",
    ("alpheus.governance", "platform_mode_head.json"): "platform_mode_head",
    ("alpheus.governance", "platform_mode_revision.json"): "platform_mode_revision",
    ("alpheus.runtime", "artifact_nonmoney.json"): "artifact",
    ("alpheus.runtime", "claim_task_command.json"): "claim_task_command",
    ("alpheus.runtime", "commit_attempt_nonmoney.json"): "commit_attempt_command",
    ("alpheus.runtime", "fail_attempt_retryable.json"): "fail_attempt_command",
    ("alpheus.runtime", "output_contract_revision.json"): "output_contract_revision",
    ("alpheus.runtime", "publication_disabled.json"): "artifact_publication_intent",
    ("alpheus.runtime", "recovery_reuse.json"): "recovery_record",
    ("alpheus.runtime", "run_queued.json"): "run",
    ("alpheus.runtime", "runtime_policy.json"): "runtime_policy",
    ("alpheus.security", "profile_set.json"): "profile_set",
}


def fail(message: str) -> None:
    raise SystemExit(f"FAIL probe=agent-contract-schema reason={message}")


def reject_duplicate_pairs(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            fail(f"duplicate-key:{key}")
        result[key] = value
    return result


def reject_float(value: str) -> object:
    fail(f"non-integer:{value}")


def load_json(path: pathlib.Path) -> object:
    return json.loads(
        path.read_text(encoding="utf-8"),
        object_pairs_hook=reject_duplicate_pairs,
        parse_float=reject_float,
        parse_constant=reject_float,
    )


def canonical_json(value: object) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")


def contract_digest(contract_type: str, value: object) -> str:
    domain = f"agent-platform.contract.{contract_type}.v1"
    preimage = PROFILE.encode() + b"\n" + domain.encode() + b"\n" + canonical_json(value)
    return hashlib.sha256(preimage).hexdigest()


def main() -> None:
    root = pathlib.Path(__file__).resolve().parent.parent
    contract_root = root / "contracts"
    manifests = sorted(contract_root.glob("*/v1/manifest.yaml"))
    if not manifests:
        fail("no-contract-packs")

    schemas: list[tuple[pathlib.Path, dict[str, object]]] = []
    schema_store: dict[str, dict[str, object]] = {}
    for manifest_path in manifests:
        manifest = load_json(manifest_path)
        if not isinstance(manifest, dict):
            fail(f"manifest-object:{manifest_path.relative_to(root)}")
        for relative in manifest["assets"]["schemas"]:
            path = manifest_path.parent / relative
            schema = load_json(path)
            if not isinstance(schema, dict):
                fail(f"schema-object:{path.relative_to(root)}")
            validator_for(schema).check_schema(schema)
            schema_id = schema.get("$id")
            if not isinstance(schema_id, str) or not schema_id:
                fail(f"schema-id:{path.relative_to(root)}")
            schema_store[schema_id] = schema
            schemas.append((path, schema))

    valid_count = 0
    digest_count = 0
    for manifest_path in manifests:
        manifest = load_json(manifest_path)
        if not isinstance(manifest, dict):
            fail(f"manifest-object:{manifest_path.relative_to(root)}")
        valid_goldens = manifest["goldens"]["valid"]
        digest_goldens = manifest["goldens"]["digests"]
        if len(valid_goldens) != len(digest_goldens):
            fail(f"golden-digest-count:{manifest_path.relative_to(root)}")
        for relative, digest_relative in zip(valid_goldens, digest_goldens):
            golden_path = manifest_path.parent / relative
            instance = load_json(golden_path)
            matches = 0
            diagnostics: list[str] = []
            for schema_path, schema in schemas:
                validator = Draft202012Validator(
                    schema,
                    resolver=RefResolver.from_schema(schema, store=schema_store),
                    format_checker=Draft202012Validator.FORMAT_CHECKER,
                )
                errors = list(validator.iter_errors(instance))
                if not errors:
                    matches += 1
                elif schema_path.parent.parent.parent == manifest_path.parent:
                    diagnostics.append(errors[0].message)
            if matches != 1:
                detail = diagnostics[0] if diagnostics else f"matches={matches}"
                fail(f"valid-golden:{golden_path.relative_to(root)}:{detail}")
            valid_count += 1

            contract_type = CONTRACT_TYPES.get((manifest["pack"], golden_path.name))
            if contract_type is None:
                fail(f"digest-domain:{golden_path.relative_to(root)}")
            expected = (manifest_path.parent / digest_relative).read_text(encoding="utf-8").strip()
            actual = contract_digest(contract_type, instance)
            if actual != expected:
                fail(f"golden-digest:{golden_path.relative_to(root)}")
            digest_count += 1

    if digest_count != len(CONTRACT_TYPES):
        fail("digest-inventory")

    print(
        json.dumps(
            {
                "status": "PASS",
                "probe": "agent-contract-schema",
                "packs": len(manifests),
                "schemas": len(schemas),
                "valid_goldens": valid_count,
                "golden_digests": digest_count,
            },
            separators=(",", ":"),
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    try:
        main()
    except (KeyError, OSError, ValueError) as error:
        fail(type(error).__name__.lower())
