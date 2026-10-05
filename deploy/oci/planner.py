#!/usr/bin/env python3
"""Create deterministic, reviewable component deployment plans."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import uuid
from datetime import datetime, timezone
from pathlib import Path

COMPONENTS = ("api", "worker", "scheduler", "operations", "admin", "dashboard", "capture", "onboarding", "keycloak")
IMAGE_KEYS = {name: name.upper() + "_IMAGE" for name in COMPONENTS}
INFRA_KEYS = ("POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE")
COMPOSITION_KEYS = set(IMAGE_KEYS.values()) | set(INFRA_KEYS)
IMAGE_REF = re.compile(r"[^\s@]+@sha256:[a-f0-9]{64}\Z")
GO_COMPONENTS = {"api", "worker", "scheduler", "operations"}
PUBLIC_PROFILE_KEYS = {
    "admin": ("API_ORIGIN", "AUTH_ORIGIN", "DASHBOARD_ORIGIN", "CAPTURE_ORIGIN"),
    "dashboard": ("API_ORIGIN", "AUTH_ORIGIN", "ADMIN_ORIGIN", "STORAGE_ORIGIN"),
    "capture": ("API_ORIGIN", "STORAGE_ORIGIN"),
    "onboarding": ("API_ORIGIN", "TURNSTILE_SITE_KEY"),
}


def canonical_hash(value: object) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def read_env(path: Path) -> dict[str, str]:
    result = {}
    for line in path.read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep and key.endswith("_IMAGE"):
            if key in result:
                raise ValueError(f"duplicate image key in active state: {key}")
            result[key] = value
    return result


def create_plan(active: dict[str, str], candidate: dict, targets: list[str], deployment_id: str | None = None) -> dict:
    if not isinstance(candidate, dict):
        raise ValueError("candidate must be a JSON object")
    if set(targets) - set(COMPONENTS) or not targets:
        raise ValueError("targets must contain one or more known components")
    if len(set(targets)) != len(targets):
        raise ValueError("duplicate target component")
    candidate_components = candidate.get("components")
    if not isinstance(candidate_components, dict):
        raise ValueError("candidate has no components object")
    if not re.fullmatch(r"[a-f0-9]{40}", str(candidate.get("sourceSha", ""))):
        raise ValueError("candidate sourceSha must be a full commit SHA")
    for component, item in candidate_components.items():
        if component not in COMPONENTS or not isinstance(item, dict):
            raise ValueError(f"invalid candidate component: {component}")
        image = item.get("image")
        if not isinstance(image, str) or not re.fullmatch(rf"ghcr\.io/[a-z0-9-]+/inspection-{component}@sha256:[a-f0-9]{{64}}", image):
            raise ValueError(f"{component} image must be digest pinned")
        if not re.fullmatch(r"[a-f0-9]{40}", str(item.get("sourceSha", ""))):
            raise ValueError(f"{component} sourceSha must be a full commit SHA")
        if not re.fullmatch(r"[a-f0-9]{64}", str(item.get("fingerprint", ""))):
            raise ValueError(f"{component} fingerprint must be SHA-256")
        if component in PUBLIC_PROFILE_KEYS:
            profile = item.get("publicProfile")
            if not isinstance(profile, dict) or set(profile) != set(PUBLIC_PROFILE_KEYS[component]):
                raise ValueError(f"{component} public build profile is incomplete")
            if any(not isinstance(value, str) or not value for value in profile.values()):
                raise ValueError(f"{component} public build profile has an empty value")
            for key, value in profile.items():
                if key == "TURNSTILE_SITE_KEY":
                    continue
                if not re.fullmatch(r"https://[A-Za-z0-9.-]+(?::[0-9]+)?", value):
                    raise ValueError(f"{component} profile {key} is not an HTTPS origin")
    absent = sorted(set(targets) - candidate_components.keys())
    if absent:
        raise ValueError("candidate does not contain selected component(s): " + ", ".join(absent))
    # Import the existing monolithic Go digest as the initial image for each
    # independent process while the initial production receipt is migrated.
    legacy_keys = {"API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE", "CAPTURE_IMAGE", "ONBOARDING_IMAGE", *INFRA_KEYS}
    if set(active) == legacy_keys:
        legacy = active["API_IMAGE"]
        active = {**active, **{IMAGE_KEYS[name]: legacy for name in GO_COMPONENTS}}
    if set(active) and set(active) != COMPOSITION_KEYS:
        missing = sorted(COMPOSITION_KEYS - set(active))
        raise ValueError("active state is incomplete: " + ", ".join(missing))
    if any(not IMAGE_REF.fullmatch(value) for value in active.values()):
        raise ValueError("active state contains a mutable or invalid image reference")
    if not active and set(targets) != set(COMPONENTS):
        raise ValueError("first deployment must select all nine components")

    desired = dict(active)
    if not active:
        infrastructure = candidate.get("infrastructure")
        if not isinstance(infrastructure, dict) or set(infrastructure) != set(INFRA_KEYS):
            raise ValueError("first deployment requires all four approved infrastructure image digests")
        for key, value in infrastructure.items():
            if not isinstance(value, str) or not IMAGE_REF.fullmatch(value):
                raise ValueError(f"{key} must be digest pinned")
            desired[key] = value
    for component, item in candidate_components.items():
        if component in targets:
            desired[IMAGE_KEYS[component]] = item["image"]
    if set(desired) != COMPOSITION_KEYS:
        raise ValueError("candidate composition is incomplete")

    compatibility = candidate.get("compatibility", {})
    if not isinstance(compatibility, dict):
        raise ValueError("candidate compatibility metadata must be an object")
    database_impact = compatibility.get("databaseChange", "unknown")
    if database_impact not in {"none", "required", "unknown"}:
        raise ValueError("candidate database compatibility is invalid")
    database_change = database_impact == "required"
    if database_change:
        required = sorted(GO_COMPONENTS)
        if not set(required).issubset(targets):
            raise ValueError("database change requires a coordinated selection of: " + ", ".join(required))
        if "operations" not in candidate_components:
            raise ValueError("database change requires operations image metadata")
    if database_impact == "unknown" and set(targets) != set(COMPONENTS):
        raise ValueError("unknown database impact requires an explicit complete composition")
    contract = compatibility.get("contractChange", "unknown")
    if contract not in {"compatible", "review-required", "breaking", "unknown"}:
        raise ValueError("candidate contract compatibility is invalid")
    if contract != "compatible" and set(targets) != set(COMPONENTS):
        raise ValueError("unverified contract change requires an explicit complete composition")

    state_hash = canonical_hash(active)
    plan = {
        "schemaVersion": 1,
        "deploymentId": deployment_id or str(uuid.uuid4()),
        "candidateId": candidate.get("candidateId"),
        "sourceSha": candidate.get("sourceSha"),
        "expectedActiveStateSha256": state_hash,
        "targets": targets,
        "desiredImages": desired,
        "publicProfiles": {name: candidate_components[name]["publicProfile"] for name in targets if name in PUBLIC_PROFILE_KEYS},
        "componentSources": {name: candidate_components[name]["sourceSha"] for name in targets if name in candidate_components},
        "operations": {"runMigration": database_change, "runSeed": database_change},
        "compatibility": compatibility,
        "createdAt": datetime.now(timezone.utc).isoformat(),
    }
    plan["planSha256"] = canonical_hash(plan)
    return plan


def create_rollback_plan(active: dict[str, str], previous: dict[str, str], targets: list[str], deployment_id: str | None = None, source_sha: str = "") -> dict:
    if set(active) != COMPOSITION_KEYS or set(previous) != COMPOSITION_KEYS:
        raise ValueError("rollback requires complete current and previous image receipts")
    if not targets or set(targets) - set(COMPONENTS) or len(set(targets)) != len(targets):
        raise ValueError("rollback targets must be unique known components")
    if any(not IMAGE_REF.fullmatch(value) for value in previous.values()):
        raise ValueError("previous composition contains a non-digest image")
    desired = dict(active)
    for component in targets:
        desired[IMAGE_KEYS[component]] = previous[IMAGE_KEYS[component]]
    plan = {
        "schemaVersion": 1,
        "deploymentId": deployment_id or str(uuid.uuid4()),
        "candidateId": "rollback-" + canonical_hash({k: previous[k] for k in sorted(previous)})[:16],
        "sourceSha": source_sha,
        "expectedActiveStateSha256": canonical_hash(active),
        "targets": targets,
        "desiredImages": desired,
        "operations": {"runMigration": False, "runSeed": False},
        "compatibility": {"databaseChange": "none", "contractChange": "recovery"},
        "createdAt": datetime.now(timezone.utc).isoformat(),
    }
    plan["planSha256"] = canonical_hash(plan)
    return plan


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--active", type=Path, required=True, help="active receipt in KEY=IMAGE format")
    parser.add_argument("--candidate", type=Path)
    parser.add_argument("--targets", required=True, help="comma-separated explicit component names")
    parser.add_argument("--deployment-id")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--rollback-from", type=Path, help="previous complete receipt; writes a rollback plan")
    parser.add_argument("--source-sha", help="source commit for the executor bundle (rollback only)")
    parser.add_argument("--bundle-sha256", help="SHA-256 of the source bundle attached to this plan")
    args = parser.parse_args()
    try:
        active = read_env(args.active) if args.active.exists() else {}
        targets = [x for x in args.targets.split(",") if x]
        if args.rollback_from:
            plan = create_rollback_plan(active, read_env(args.rollback_from), targets, args.deployment_id, args.source_sha or "")
        else:
            if not args.candidate:
                raise ValueError("--candidate is required unless --rollback-from is set")
            candidate = json.loads(args.candidate.read_text())
            plan = create_plan(active, candidate, targets, args.deployment_id)
        if args.bundle_sha256:
            if not re.fullmatch(r"[a-f0-9]{64}", args.bundle_sha256):
                raise ValueError("bundle SHA-256 is invalid")
            plan["bundleSha256"] = args.bundle_sha256
            plan["planSha256"] = canonical_hash({k:v for k,v in plan.items() if k != "planSha256"})
        args.output.write_text(json.dumps(plan, indent=2, sort_keys=True) + "\n")
        print(json.dumps({"planSha256": plan["planSha256"], "deploymentId": plan["deploymentId"], "targets": plan["targets"]}, sort_keys=True))
        return 0
    except (ValueError, OSError, json.JSONDecodeError) as exc:
        print(f"plan rejected: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
