#!/usr/bin/env python3
"""Verify an OCI release bundle against its source commit and digest manifest."""

from __future__ import annotations

import argparse
import hashlib
import re
import subprocess
import tarfile
from pathlib import Path, PurePosixPath


IMAGE_KEYS = {
    "API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE",
    "CAPTURE_IMAGE", "ONBOARDING_IMAGE", "POSTGRES_IMAGE",
    "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE",
}
OPTIONAL_IMAGE_KEYS = {"WORKER_IMAGE", "SCHEDULER_IMAGE", "OPERATIONS_IMAGE"}
PUBLIC_KEYS = {
    "NEXT_PUBLIC_INSPECTION_API_URL", "NEXT_PUBLIC_OIDC_AUTHORIZE_URL",
    "NEXT_PUBLIC_OIDC_TOKEN_URL", "NEXT_PUBLIC_DASHBOARD_URL",
    "NEXT_PUBLIC_ADMIN_URL", "NEXT_PUBLIC_CAPTURE_URL",
    "NEXT_PUBLIC_STORAGE_URL", "NEXT_PUBLIC_TURNSTILE_SITE_KEY",
    "NEXT_PUBLIC_CAPTURE_IMAGE_OPTIMIZATION",
}
SHA = re.compile(r"[a-f0-9]{40}\Z")
IMAGE = re.compile(r"[^\s@]+@sha256:[a-f0-9]{64}\Z")


def verify(bundle: str, manifest: str, checksum_file: str, release: str) -> None:
    if not SHA.fullmatch(release):
        raise ValueError("release must be a 40-character lowercase commit SHA")
    digest = hashlib.sha256(Path(bundle).read_bytes()).hexdigest()
    expected_digest = Path(checksum_file).read_text(encoding="ascii").split()[0]
    if not re.fullmatch(r"[a-f0-9]{64}", expected_digest) or digest != expected_digest:
        raise ValueError("release bundle checksum does not match its sidecar")

    values = {}
    for line in Path(manifest).read_text(encoding="utf-8").splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, sep, value = line.partition("=")
        if not sep or key in values:
            raise ValueError("release manifest has an invalid or duplicate assignment")
        values[key] = value
    allowed_keys = IMAGE_KEYS | OPTIONAL_IMAGE_KEYS | PUBLIC_KEYS | {"RELEASE_SHA", "BUNDLE_SHA256"}
    if set(values) - allowed_keys:
        raise ValueError("release manifest contains unexpected keys")
    if values.get("RELEASE_SHA") != release or values.get("BUNDLE_SHA256") != digest:
        raise ValueError("release manifest SHA or bundle checksum does not match")
    if not IMAGE_KEYS.issubset(values) or any(not IMAGE.fullmatch(values[key]) for key in IMAGE_KEYS):
        raise ValueError("every release image must be pinned by sha256 digest")
    if any(key in values and not IMAGE.fullmatch(values[key]) for key in OPTIONAL_IMAGE_KEYS):
        raise ValueError("optional process images must be pinned by sha256 digest")

    tracked = subprocess.check_output(
        ["git", "ls-tree", "-r", "--name-only", release, "--", "deploy/oci", "deploy/keycloak"],
        text=True,
    ).splitlines()
    expected = {path for path in tracked if ".terraform/" not in path and "/releases/" not in path}
    seen = set()
    with tarfile.open(bundle, "r:gz") as archive:
        for member in archive.getmembers():
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts:
                raise ValueError(f"unsafe or unsupported bundle member: {member.name}")
            if member.isdir():
                continue
            if not member.isfile():
                raise ValueError(f"unsafe or unsupported bundle member: {member.name}")
            if member.name not in expected or member.name in seen:
                raise ValueError(f"bundle member is untracked, duplicated, or unexpected: {member.name}")
            source = subprocess.check_output(["git", "show", f"{release}:{member.name}"])
            content = archive.extractfile(member).read()
            if content != source:
                raise ValueError(f"bundle member does not match source commit: {member.name}")
            seen.add(member.name)
    if seen != expected:
        raise ValueError(f"bundle is incomplete; missing tracked files: {', '.join(sorted(expected - seen))}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--checksum", required=True)
    parser.add_argument("--release", required=True)
    args = parser.parse_args()
    verify(args.bundle, args.manifest, args.checksum, args.release)
    print(f"Verified source commit, bundle checksum and immutable image digests for {args.release}.")


if __name__ == "__main__":
    main()
