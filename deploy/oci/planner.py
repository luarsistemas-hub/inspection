#!/usr/bin/env python3
"""Create deterministic, reviewable component deployment plans."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import uuid
import subprocess
from datetime import datetime, timezone
from pathlib import Path

CATALOG = Path(__file__).with_name("components.json")
CATALOG_DATA = json.loads(CATALOG.read_text())
COMPONENTS = tuple(CATALOG_DATA["components"])
IMAGE_KEYS = {name: spec["image"] for name, spec in CATALOG_DATA["components"].items()}
COMPOSE_SERVICES = {name: spec["service"] for name, spec in CATALOG_DATA["components"].items()}
INFRA_KEYS = ("POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE")
COMPOSITION_KEYS = set(IMAGE_KEYS.values()) | set(INFRA_KEYS)
PREVIOUS_COMPOSITION_KEYS = COMPOSITION_KEYS - {"LITELLM_IMAGE"}
LEGACY_OPTIONAL_COMPOSITION_KEYS = COMPOSITION_KEYS - {"VIDEO_WORKER_IMAGE"}
LEGACY_OLD_COMPOSITION_KEYS = COMPOSITION_KEYS - {"LITELLM_IMAGE", "VIDEO_WORKER_IMAGE"}
IMAGE_REF = re.compile(r"[^\s@]+@sha256:[a-f0-9]{64}\Z")
GO_COMPONENTS = {"api", "worker", "scheduler", "video-worker", "operations"}
DATABASE_COMPATIBILITY_PATHS = ("services/inspection/internal/platform/database/migrations", "services/inspection/internal/features/analysis/seed_prompt", "services/inspection/internal/features/templates/catalog/seeds.go", "services/inspection/cmd/inspection-seed", "services/inspection/cmd/inspection-prompt-seed")
CONTRACT_COMPATIBILITY_PATHS = ("services/inspection/schema.graphqls", "services/inspection/internal/platform/graphql", "services/inspection/internal/contracts/events")
PUBLIC_PROFILE_KEYS = {
    "admin": ("API_ORIGIN", "AUTH_ORIGIN", "DASHBOARD_ORIGIN", "CAPTURE_ORIGIN"),
    "dashboard": ("API_ORIGIN", "AUTH_ORIGIN", "ADMIN_ORIGIN", "STORAGE_ORIGIN"),
    "capture": ("API_ORIGIN", "STORAGE_ORIGIN"),
    "onboarding": ("API_ORIGIN", "TURNSTILE_SITE_KEY"),
}


def canonical_hash(value: object) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def runtime_config_sha256(root: Path) -> str:
    """Hash runtime configuration files in stable path order."""
    root = root.resolve()
    required = [root / "deploy/oci" / name for name in
                ("compose.yaml", "Caddyfile", "litellm-config.yaml", "keycloak-realm.json")]
    if any(not path.is_file() for path in required) or not (root / "deploy/keycloak").is_dir():
        raise ValueError("runtime configuration source is incomplete")
    files = list(required)
    keycloak = root / "deploy/keycloak"
    if keycloak.exists():
        files.extend(path for path in keycloak.rglob("*") if path.is_file() and "themes" not in path.relative_to(keycloak).parts)
    digest = hashlib.sha256()
    for path in sorted(set(files), key=lambda item: item.relative_to(root).as_posix()):
        if not path.is_file():
            continue
        digest.update(path.relative_to(root).as_posix().encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def read_env(path: Path) -> dict[str, str]:
    result = {}
    for line in path.read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep and key.endswith("_IMAGE"):
            if key in result:
                raise ValueError(f"duplicate image key in active state: {key}")
            result[key] = value
    return result


def read_sources(path: Path) -> dict[str, str]:
    result = {}
    for line in path.read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep and key.startswith("COMPONENT_SOURCE_"):
            component = key.removeprefix("COMPONENT_SOURCE_").lower().replace("_", "-")
            if component in result:
                raise ValueError(f"duplicate component source in active state: {component}")
            result[component] = value
    return result


def read_receipt_value(path: Path, name: str) -> str:
    for line in path.read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep and key == name:
            return value
    return ""


def infer_compatibility(candidate: dict, active_sources: dict[str, str] | None, repo: Path | None) -> dict:
    if not active_sources or set(active_sources) != set(COMPONENTS) or repo is None:
        return {"databaseChange": "unknown", "contractChange": "unknown"}
    current = candidate["sourceSha"]
    try:
        # A change from any active component's origin must be considered, even
        # when the most recently registered candidate did not include it.
        database = "none"
        contract = "compatible"
        for origin in active_sources.values():
            if not re.fullmatch(r"[a-f0-9]{40}", origin):
                return {"databaseChange": "unknown", "contractChange": "unknown"}
            db = subprocess.run(["git", "diff", "--quiet", origin, current, "--", *DATABASE_COMPATIBILITY_PATHS], cwd=repo).returncode
            api = subprocess.run(["git", "diff", "--quiet", origin, current, "--", *CONTRACT_COMPATIBILITY_PATHS], cwd=repo).returncode
            if db not in (0, 1) or api not in (0, 1):
                return {"databaseChange": "unknown", "contractChange": "unknown"}
            if db == 1: database = "required"
            if api == 1: contract = "review-required"
        return {"databaseChange": database, "contractChange": contract}
    except OSError:
        return {"databaseChange": "unknown", "contractChange": "unknown"}


def create_plan(active: dict[str, str], candidate: dict, targets: list[str] | None = None, deployment_id: str | None = None, *, scope: str = "selected", active_sources: dict[str, str] | None = None, repo: Path | None = None, operation: str = "deploy", recovery_of: dict | None = None) -> dict:
    if not isinstance(candidate, dict):
        raise ValueError("candidate must be a JSON object")
    if scope not in {"selected", "all", "changed"}:
        raise ValueError("scope must be selected, changed, or all")
    targets = list(targets or [])
    if set(targets) - set(COMPONENTS) or (scope == "selected" and not targets):
        raise ValueError("targets must contain one or more known components")
    if len(set(targets)) != len(targets):
        raise ValueError("duplicate target component")
    candidate_components = candidate.get("components")
    if not isinstance(candidate_components, dict):
        raise ValueError("candidate has no components object")
    if set(candidate_components) != set(COMPONENTS):
        missing = sorted(set(COMPONENTS) - set(candidate_components))
        raise ValueError("candidate composition is incomplete; missing: " + ", ".join(missing))
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
    compatibility = infer_compatibility(candidate, active_sources, repo) if active else {"databaseChange": "required", "contractChange": "unknown"}
    if active and (active_sources is None or set(active_sources) != set(COMPONENTS)):
        compatibility = {"databaseChange": "unknown", "contractChange": "unknown"}
    if not isinstance(compatibility, dict):
        raise ValueError("candidate compatibility metadata must be an object")
    database_impact = compatibility.get("databaseChange", "unknown")
    contract = compatibility.get("contractChange", "unknown")
    if database_impact not in {"none", "required", "unknown"}:
        raise ValueError("candidate database compatibility is invalid")
    if contract not in {"compatible", "review-required", "breaking", "unknown"}:
        raise ValueError("candidate contract compatibility is invalid")
    if scope == "all":
        targets = list(COMPONENTS)
    elif scope == "changed":
        if not active:
            targets = list(COMPONENTS)
        else:
            targets = [name for name in COMPONENTS if active.get(IMAGE_KEYS[name]) != candidate_components[name]["image"]]
            infrastructure = candidate.get("infrastructure", {})
            if isinstance(infrastructure, dict) and active.get("LITELLM_IMAGE") != infrastructure.get("LITELLM_IMAGE"):
                if "worker" not in targets:
                    targets.append("worker")
            if contract != "compatible" or database_impact == "unknown":
                targets = list(COMPONENTS)
            elif database_impact == "required":
                targets.extend(name for name in GO_COMPONENTS if name not in targets)
            if active_sources is None or set(active_sources) != set(COMPONENTS):
                targets = list(COMPONENTS)
    if set(targets) & GO_COMPONENTS:
        targets = [name for name in COMPONENTS if name in (set(targets) | GO_COMPONENTS)]
    if not targets:
        raise ValueError("candidate has no image digest changes relative to active state")
    # Import the existing monolithic Go digest as the initial image for each
    # independent process while the initial production receipt is migrated.
    legacy_keys = {"API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE", "CAPTURE_IMAGE", "ONBOARDING_IMAGE", *(set(INFRA_KEYS) - {"LITELLM_IMAGE"})}
    if set(active) == legacy_keys:
        legacy = active["API_IMAGE"]
        active = {**active, **{IMAGE_KEYS[name]: legacy for name in GO_COMPONENTS - {"video-worker"}}}
    if set(active) and set(active) not in (COMPOSITION_KEYS, PREVIOUS_COMPOSITION_KEYS, LEGACY_OPTIONAL_COMPOSITION_KEYS, LEGACY_OLD_COMPOSITION_KEYS):
        missing = sorted(COMPOSITION_KEYS - set(active))
        raise ValueError("active state is incomplete: " + ", ".join(missing))
    if any(not IMAGE_REF.fullmatch(value) for value in active.values()):
        raise ValueError("active state contains a mutable or invalid image reference")
    if active and ("VIDEO_WORKER_IMAGE" not in active or "LITELLM_IMAGE" not in active) and set(targets) != set(COMPONENTS):
        raise ValueError("upgrading a pre-video or legacy receipt requires selecting the complete ten-component composition")
    if not active and set(targets) != set(COMPONENTS):
        raise ValueError("first deployment must select all ten components")
    if active and "LITELLM_IMAGE" not in active and "worker" not in targets:
        raise ValueError("introducing the LiteLLM gateway requires selecting worker")

    desired = dict(active)
    infrastructure = candidate.get("infrastructure")
    if not isinstance(infrastructure, dict) or set(infrastructure) != set(INFRA_KEYS):
        raise ValueError("candidate requires all five approved infrastructure image digests")
    if any(not isinstance(value, str) or not IMAGE_REF.fullmatch(value) for value in infrastructure.values()):
        raise ValueError("infrastructure images must be digest pinned")
    if not active:
        desired.update(infrastructure)
    elif "LITELLM_IMAGE" not in desired or "worker" in targets:
        desired["LITELLM_IMAGE"] = infrastructure["LITELLM_IMAGE"]
    for component, item in candidate_components.items():
        if component in targets:
            desired[IMAGE_KEYS[component]] = item["image"]
    if set(desired) not in (COMPOSITION_KEYS, PREVIOUS_COMPOSITION_KEYS, LEGACY_OPTIONAL_COMPOSITION_KEYS, LEGACY_OLD_COMPOSITION_KEYS):
        raise ValueError("candidate composition is incomplete")

    database_change = database_impact != "none"
    if database_change:
        required = sorted(GO_COMPONENTS)
        if not set(required).issubset(targets):
            raise ValueError("database change requires a coordinated selection of: " + ", ".join(required))
        if "operations" not in candidate_components:
            raise ValueError("database change requires operations image metadata")
    if database_impact == "unknown" and set(targets) != set(COMPONENTS):
        raise ValueError("unknown database impact requires an explicit complete composition")
    if contract != "compatible" and set(targets) != set(COMPONENTS):
        raise ValueError("unverified contract change requires an explicit complete composition")

    state_hash = canonical_hash(active)
    if operation not in {"deploy", "recovery"}:
        raise ValueError("unsupported deployment operation")
    if operation == "recovery" and (set(targets) != set(COMPONENTS) or not isinstance(recovery_of, dict) or set(recovery_of) != {"deploymentId", "planSha256"}):
        raise ValueError("recovery requires the complete composition and blocked deployment reference")
    config_root = repo or Path(__file__).resolve().parents[2]
    runtime_hash = runtime_config_sha256(config_root)
    if operation == "recovery":
        runtime_hash = runtime_hash or candidate.get("runtimeConfigSha256")
        if not isinstance(runtime_hash, str) or not re.fullmatch(r"[a-f0-9]{64}", runtime_hash):
            raise ValueError("recovery requires runtime configuration hash")
    plan = {
        "schemaVersion": 2,
        "operation": operation,
        "deploymentId": deployment_id or str(uuid.uuid4()),
        "candidateId": candidate.get("candidateId"),
        "sourceSha": candidate.get("sourceSha"),
        "expectedActiveStateSha256": state_hash,
        "targets": targets,
        "desiredImages": desired,
        "publicProfiles": {name: candidate_components[name]["publicProfile"] for name in targets if name in PUBLIC_PROFILE_KEYS},
        "componentSources": {name: candidate_components[name]["sourceSha"] for name in COMPONENTS},
        "operations": {"runMigration": bool(set(targets) & GO_COMPONENTS) or operation == "recovery", "runSeed": bool(set(targets) & GO_COMPONENTS) or operation == "recovery"},
        **({"runtimeConfigSha256": runtime_hash} if runtime_hash else {}),
        **({"recoveryOf": recovery_of} if recovery_of else {}),
        "compatibility": compatibility,
        "createdAt": datetime.now(timezone.utc).isoformat(),
    }
    plan["planSha256"] = canonical_hash(plan)
    return plan


def create_rollback_plan(active: dict[str, str], previous: dict[str, str], targets: list[str], deployment_id: str | None = None, source_sha: str = "", *, active_sources: dict[str, str] | None = None, previous_sources: dict[str, str] | None = None, runtime_config_sha: str | None = None, active_runtime_config_sha: str | None = None) -> dict:
    if set(active) not in (COMPOSITION_KEYS, LEGACY_OPTIONAL_COMPOSITION_KEYS) or set(previous) not in (COMPOSITION_KEYS, PREVIOUS_COMPOSITION_KEYS, LEGACY_OPTIONAL_COMPOSITION_KEYS, LEGACY_OLD_COMPOSITION_KEYS):
        raise ValueError("rollback requires complete current and previous image receipts")
    if not targets or set(targets) - set(COMPONENTS) or len(set(targets)) != len(targets):
        raise ValueError("rollback targets must be unique known components")
    if any(not IMAGE_REF.fullmatch(value) for value in previous.values()):
        raise ValueError("previous composition contains a non-digest image")
    if set(targets) & GO_COMPONENTS:
        targets = [name for name in COMPONENTS if name in (set(targets) | GO_COMPONENTS)]
    if "VIDEO_WORKER_IMAGE" in active and "VIDEO_WORKER_IMAGE" not in previous and set(targets) & GO_COMPONENTS:
        raise ValueError("rollback target has no video-worker image; rollback is unsupported")
    if not active_runtime_config_sha or not runtime_config_sha or active_runtime_config_sha != runtime_config_sha:
        raise ValueError("automatic rollback requires matching runtime configuration hashes; create a corrective plan")
    if not re.fullmatch(r"[a-f0-9]{40}", source_sha):
        raise ValueError("rollback executor source SHA must come from the active receipt")
    desired = dict(active)
    if "LITELLM_IMAGE" not in previous:
        previous = {**previous, "LITELLM_IMAGE": active["LITELLM_IMAGE"]}
    for component in targets:
        desired[IMAGE_KEYS[component]] = previous[IMAGE_KEYS[component]]
    component_sources = None
    if active_sources and previous_sources and set(active_sources) == set(COMPONENTS) and set(previous_sources) == set(COMPONENTS):
        merged_sources = dict(active_sources)
        merged_sources.update({name: previous_sources[name] for name in targets})
        if all(re.fullmatch(r"[a-f0-9]{40}", value) for value in merged_sources.values()):
            component_sources = merged_sources
    plan = {
        "schemaVersion": 2,
        "operation": "rollback",
        "deploymentId": deployment_id or str(uuid.uuid4()),
        "candidateId": "rollback-" + canonical_hash({k: previous[k] for k in sorted(previous)})[:16],
        "sourceSha": source_sha,
        "runtimeConfigSha256": runtime_config_sha,
        "expectedActiveStateSha256": canonical_hash(active),
        "targets": targets,
        "desiredImages": desired,
        "rollbackTargetHasVideoWorker": "VIDEO_WORKER_IMAGE" in previous,
        **({"componentSources": component_sources} if component_sources is not None else {}),
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
    parser.add_argument("--scope", choices=("selected", "changed", "all"), default="selected")
    parser.add_argument("--repo", type=Path, help="repository used to compare active origins with candidate source")
    parser.add_argument("--bundle-sha256", help="SHA-256 of the source bundle attached to this plan")
    parser.add_argument("--recovery-of", help="blocked deployment reference as deploymentId,planSha256")
    parser.add_argument("--runtime-config-sha256", help="runtime config hash for an archived executor bundle")
    args = parser.parse_args()
    try:
        active = read_env(args.active) if args.active.exists() else {}
        targets = [x for x in args.targets.split(",") if x]
        if args.rollback_from:
            previous_path = args.rollback_from
            runtime_hash = args.runtime_config_sha256 or (runtime_config_sha256(args.repo) if args.repo else None)
            plan = create_rollback_plan(active, read_env(previous_path), targets, args.deployment_id,
                args.source_sha or "", active_sources=read_sources(args.active) if args.active.exists() else None,
                previous_sources=read_sources(previous_path), runtime_config_sha=runtime_hash,
                active_runtime_config_sha=read_receipt_value(args.active, "RUNTIME_CONFIG_SHA256") if args.active.exists() else "")
        else:
            if not args.candidate:
                raise ValueError("--candidate is required unless --rollback-from is set")
            candidate = json.loads(args.candidate.read_text())
            active_sources = read_sources(args.active) if args.active and args.active.exists() else None
            recovery_of = None
            operation = "deploy"
            if args.recovery_of:
                fields = args.recovery_of.split(",")
                if len(fields) != 2:
                    raise ValueError("--recovery-of must be deploymentId,planSha256")
                recovery_of = {"deploymentId": fields[0], "planSha256": fields[1]}
                operation = "recovery"
            plan = create_plan(active, candidate, targets, args.deployment_id, scope=args.scope,
                active_sources=active_sources, repo=args.repo, operation=operation, recovery_of=recovery_of)
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
