#!/usr/bin/env python3
"""Resolve build components from changed files; fail closed on unknown build inputs."""
from __future__ import annotations

import argparse
import fnmatch
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "deploy/oci/components.json"


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()


def matches(path: str, patterns: list[str]) -> bool:
    return any(path == p or path.startswith(p) if p.endswith("/") else fnmatch.fnmatch(path, p) for p in patterns)


def go_inputs(component: str, command: str) -> set[str]:
    env = os.environ.copy()
    env.update({"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"})
    proc = subprocess.run(
        ["go", "list", "-deps", "-json", f"./cmd/{command}"],
        cwd=ROOT / "services/inspection", text=True, capture_output=True, check=True, env=env,
    )
    # go list emits concatenated JSON objects, not a JSON array.
    decoder = json.JSONDecoder()
    source = proc.stdout
    packages = []
    while source.strip():
        source = source.lstrip()
        item, end = decoder.raw_decode(source)
        packages.append(item)
        source = source[end:]
    result: set[str] = set()
    for package in packages:
        for field in ("GoFiles", "CgoFiles", "EmbedFiles", "TestGoFiles", "XTestGoFiles"):
            for name in package.get(field, []):
                path = (Path(package["Dir"]) / name).resolve()
                try:
                    result.add(str(path.relative_to(ROOT)))
                except ValueError:
                    # Third-party module sources live outside this repository;
                    # their selected versions are represented by go.sum.
                    continue
    # Toolchain/module inputs and the component Docker recipe affect every Go image.
    result.update({"go.mod", "go.sum", "go.work", "go.work.sum", ".dockerignore", "deploy/oci/Go.Dockerfile", "deploy/oci/base-images.lock", "deploy/oci/affected.py", ".github/workflows/build-images.yml"})
    return result


def input_files(component: str, catalog: dict) -> set[str]:
    spec = catalog["components"][component]
    if spec["kind"] in ("go", "go-tools"):
        commands = spec.get("commands", [spec.get("command")])
        files = set()
        for command in commands:
            files.update(go_inputs(component, command))
    else:
        files = {p for p in git("ls-files").splitlines() if matches(p, spec["paths"])}
    files.update({".dockerignore", "deploy/oci/components.json", "deploy/oci/compose.yaml", "deploy/oci/base-images.lock", "deploy/oci/affected.py", ".github/workflows/build-images.yml"})
    dockerfile = {"go": "deploy/oci/Go.Dockerfile", "go-tools": "deploy/oci/Go.Dockerfile", "frontend": f"apps/{component}/Dockerfile", "keycloak": "deploy/oci/Dockerfile.keycloak"}[spec["kind"]]
    files.add(dockerfile)
    return {p for p in files if (ROOT / p).is_file()}


def fingerprint(component: str, catalog: dict | None = None, profile: dict | None = None) -> str:
    catalog = catalog or json.loads(CATALOG.read_text())
    digest = hashlib.sha256()
    for path in sorted(input_files(component, catalog)):
        digest.update(path.encode() + b"\0" + (ROOT / path).read_bytes() + b"\0")
    allowed = {
        "admin": {"API_ORIGIN", "AUTH_ORIGIN", "DASHBOARD_ORIGIN", "CAPTURE_ORIGIN"},
        "dashboard": {"API_ORIGIN", "AUTH_ORIGIN", "ADMIN_ORIGIN", "STORAGE_ORIGIN"},
        "capture": {"API_ORIGIN", "STORAGE_ORIGIN"},
        "onboarding": {"API_ORIGIN", "TURNSTILE_SITE_KEY"},
    }.get(component, set())
    normalized_profile = {key: value for key, value in (profile or {}).items() if key in allowed}
    digest.update(json.dumps(normalized_profile, sort_keys=True, separators=(",", ":")).encode())
    return digest.hexdigest()


def resolve(base: str, head: str, requested_scope: str = "changed", targets: list[str] | None = None) -> dict:
    catalog = json.loads(CATALOG.read_text())
    components = catalog["components"]
    unknown_targets = sorted(set(targets or []) - set(components))
    if unknown_targets:
        raise ValueError(f"Unknown component(s): {', '.join(unknown_targets)}")
    if requested_scope == "all":
        selected = sorted(components)
        changed = []
    else:
        changed = git("diff", "--name-only", "--diff-filter=ACDMRTUXB", f"{base}...{head}").splitlines() if base else []
        selected: set[str] = set()
        recognized: set[str] = set()
        # Resolve Go executables precisely using their transitive source and embed inputs.
        go_components = [name for name, spec in components.items() if spec["kind"] in ("go", "go-tools")]
        go_file_sets = {
            name: set().union(*(go_inputs(name, command) for command in components[name].get("commands", [components[name].get("command", "")])))
            for name in go_components
        } if changed else {}
        for path in changed:
            for name, spec in components.items():
                if spec["kind"] in ("go", "go-tools"):
                    if path in go_file_sets[name]:
                        selected.add(name)
                        recognized.add(path)
                elif matches(path, spec["paths"]):
                    selected.add(name)
                    recognized.add(path)
            for rule in catalog["sharedRules"]:
                if matches(path, rule["paths"]):
                    selected.update(rule["components"])
                    recognized.add(path)
            if matches(path, catalog["ignoredPaths"]):
                recognized.add(path)
        unknown = sorted(set(changed) - recognized)
        if unknown:
            raise ValueError("Unclassified changed path(s); refusing automatic selection: " + ", ".join(unknown))
        if requested_scope == "selected":
            selected = set(targets or [])
        elif targets:
            selected.intersection_update(targets)
        selected = sorted(selected)
    return {"base": base, "head": head, "scope": requested_scope, "changedPaths": changed, "targets": selected}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="")
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--scope", choices=("changed", "selected", "all"), default="changed")
    parser.add_argument("--targets", default="", help="comma-separated components")
    args = parser.parse_args()
    try:
        head = git("rev-parse", args.head)
        base = git("rev-parse", args.base) if args.base else ""
        if args.scope == "changed" and not base:
            raise ValueError("--base is required for scope=changed")
        result = resolve(base, head, args.scope, [x for x in args.targets.split(",") if x])
        print(json.dumps(result, sort_keys=True))
        return 0
    except (ValueError, subprocess.CalledProcessError) as exc:
        print(f"component selection failed: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
