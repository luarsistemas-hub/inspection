#!/usr/bin/env python3
"""Create the Inspection runtime secrets in OCI Vault from a local private file."""

import argparse
import base64
import binascii
import hmac
import json
import os
import re
import stat
import sys
import tempfile
import uuid
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
NAMES_FILE = Path(__file__).with_name("secrets.map.example")
VAULT_NAME = "inspection-vault"
KEY_NAME = "inspection-secrets-key"


class VaultInputError(Exception):
    """A local input or selected OCI resource is unsuitable for upload."""


def expected_names():
    names = []
    for line in NAMES_FILE.read_text(encoding="utf-8").splitlines():
        if line and not line.startswith("#"):
            names.append(line.split()[0])
    if len(names) != 21 or len(set(names)) != len(names):
        raise VaultInputError("secrets.map.example must contain 21 unique names")
    return names


def outside_repository(path):
    resolved = path.expanduser().resolve()
    if os.path.commonpath((str(ROOT), str(resolved))) == str(ROOT):
        raise VaultInputError("keep values and generated maps outside the repository")
    return resolved


def private_directory(path):
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    metadata = path.stat()
    if not stat.S_ISDIR(metadata.st_mode) or metadata.st_uid != os.getuid():
        raise VaultInputError("the output directory must belong to the current user")
    if metadata.st_mode & 0o077:
        raise VaultInputError("the output directory must have mode 0700")


def write_template(path, names):
    path = outside_repository(path)
    private_directory(path.parent)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(path, flags, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as target:
        target.write("# Fill each value after =. Do not add quotes or use 'export'.\n")
        target.write("# Keep each value on one line and do not use apostrophes.\n")
        for name in names:
            target.write(f"{name}=\n")
    print(f"Template created with mode 0600: {path}")


def read_values(path, names):
    path = outside_repository(path)
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(path, flags)
    try:
        metadata = os.fstat(fd)
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != os.getuid():
            raise VaultInputError("the values file must be a regular file owned by you")
        if metadata.st_mode & 0o077:
            raise VaultInputError("the values file must have mode 0600")
        if metadata.st_size > 65536:
            raise VaultInputError("the values file is too large")
        with os.fdopen(fd, "r", encoding="utf-8") as source:
            fd = -1
            lines = source.read().splitlines()
    finally:
        if fd >= 0:
            os.close(fd)

    values = {}
    for number, line in enumerate(lines, 1):
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise VaultInputError(f"invalid assignment on line {number}")
        name, value = line.split("=", 1)
        if name not in names or name in values:
            raise VaultInputError(f"unknown or repeated name on line {number}")
        if not value or value != value.strip() or any(c in value for c in ("'", "\r", "\x00")):
            raise VaultInputError(f"invalid or empty value for {name}")
        values[name] = value

    missing = [name for name in names if name not in values]
    if missing:
        raise VaultInputError("missing values: " + ", ".join(missing))

    pepper = values["INSPECTION_OTP_PEPPER"]
    if not re.fullmatch(r"(?:[0-9a-fA-F]{2}){32,}", pepper):
        raise VaultInputError("INSPECTION_OTP_PEPPER must be at least 32 random bytes in hex")
    try:
        payload_keys = json.loads(values["NOTIFICATION_PAYLOAD_KEYS"])
    except json.JSONDecodeError:
        raise VaultInputError("NOTIFICATION_PAYLOAD_KEYS must be a JSON object") from None
    if not isinstance(payload_keys, dict) or not payload_keys:
        raise VaultInputError("NOTIFICATION_PAYLOAD_KEYS must contain at least one key")
    for key_id, encoded in payload_keys.items():
        if not isinstance(key_id, str) or not key_id or not isinstance(encoded, str):
            raise VaultInputError("invalid NOTIFICATION_PAYLOAD_KEYS entry")
        try:
            key_bytes = base64.b64decode(encoded, validate=True)
        except (ValueError, binascii.Error):
            raise VaultInputError("NOTIFICATION_PAYLOAD_KEYS contains invalid Base64") from None
        if len(key_bytes) != 32:
            raise VaultInputError("each NOTIFICATION_PAYLOAD_KEYS key must be 32 bytes")
    if values["NOTIFICATION_ACTIVE_PAYLOAD_KEY"] not in payload_keys:
        raise VaultInputError("active notification key ID is absent from NOTIFICATION_PAYLOAD_KEYS")
    return values


def oci_clients(args):
    try:
        import oci
    except ImportError:
        raise VaultInputError("install the OCI Python SDK from deploy/oci/requirements-vault.txt") from None

    config = oci.config.from_file(file_location=args.config_file, profile_name=args.profile)
    config["region"] = args.region
    vault = oci.key_management.KmsVaultClient(config).get_vault(args.vault_id).data
    if (vault.display_name != VAULT_NAME or vault.compartment_id != args.compartment_id
            or vault.lifecycle_state != "ACTIVE"):
        raise VaultInputError("the selected vault is not the active Inspection vault in this compartment")

    kms = oci.key_management.KmsManagementClient(config, vault.management_endpoint)
    keys = oci.pagination.list_call_get_all_results(kms.list_keys, args.compartment_id).data
    matches = [key for key in keys if key.display_name == KEY_NAME and key.vault_id == args.vault_id]
    if len(matches) != 1:
        raise VaultInputError("expected exactly one inspection-secrets-key in the selected vault")
    key = kms.get_key(matches[0].id).data
    if key.lifecycle_state != "ENABLED" or key.compartment_id != args.compartment_id:
        raise VaultInputError("the Inspection encryption key is not enabled in this compartment")

    vault_client = oci.vault.VaultsClient(config)
    secrets_client = oci.secrets.SecretsClient(config)
    return oci, vault_client, secrets_client, key.id


def existing_secrets(oci, vault_client, secrets_client, args, names, values, key_id):
    summaries = oci.pagination.list_call_get_all_results(
        vault_client.list_secrets, args.compartment_id, vault_id=args.vault_id
    ).data
    by_name = {}
    for summary in summaries:
        if summary.secret_name in names and summary.lifecycle_state != "DELETED":
            if summary.secret_name in by_name:
                raise VaultInputError(f"duplicate OCI secret name: {summary.secret_name}")
            by_name[summary.secret_name] = summary
    for name, summary in by_name.items():
        if summary.lifecycle_state != "ACTIVE":
            raise VaultInputError(f"{name} is not ACTIVE in OCI")
        if summary.key_id != key_id:
            raise VaultInputError(f"{name} uses a different encryption key in OCI")
        bundle = secrets_client.get_secret_bundle(summary.id).data
        current = base64.b64decode(bundle.secret_bundle_content.content, validate=True)
        if not hmac.compare_digest(current, values[name].encode("utf-8")):
            raise VaultInputError(f"{name} exists in OCI with a different value; review it manually")
    return by_name


def write_private(path, content):
    fd, temporary = tempfile.mkstemp(prefix=".inspection-", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as target:
            target.write(content)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def upload(args, names, values):
    output_dir = outside_repository(Path(args.output_dir))
    oci, vault_client, secrets_client, key_id = oci_clients(args)
    existing = existing_secrets(oci, vault_client, secrets_client, args, names, values, key_id)
    missing = [name for name in names if name not in existing]
    print(f"Vault {VAULT_NAME}: {len(existing)} matching secrets, {len(missing)} to create")
    for name in missing:
        print(f"  create {name}")
    if not args.apply:
        print("Preview only. Rerun with --apply to create the missing secrets.")
        return

    private_directory(output_dir)
    ids = {name: summary.id for name, summary in existing.items()}
    for name in missing:
        content = base64.b64encode(values[name].encode("utf-8")).decode("ascii")
        details = oci.vault.models.CreateSecretDetails(
            compartment_id=args.compartment_id,
            vault_id=args.vault_id,
            key_id=key_id,
            secret_name=name,
            secret_content=oci.vault.models.Base64SecretContentDetails(content=content, stage="CURRENT"),
        )
        response = vault_client.create_secret(details, opc_retry_token=uuid.uuid4().hex)
        ready = oci.wait_until(
            vault_client, vault_client.get_secret(response.data.id),
            evaluate_response=lambda item: item.data.lifecycle_state in ("ACTIVE", "FAILED"),
            max_wait_seconds=300,
        ).data
        if ready.lifecycle_state != "ACTIVE":
            raise VaultInputError(f"{name} was created but did not become ACTIVE")
        ids[name] = ready.id
        print(f"  created {name}")

    if len(ids) != len(names) or any(not ids[name].startswith("ocid1.vaultsecret.") for name in names):
        raise VaultInputError("OCI did not return all expected secret OCIDs")
    map_path = output_dir / "secrets.map"
    json_path = output_dir / "secret_ocids.json"
    write_private(map_path, "# ENV_NAME OCI_VAULT_SECRET_OCID\n" + "".join(
        f"{name} {ids[name]}\n" for name in names
    ))
    write_private(json_path, json.dumps({name: ids[name] for name in names}, indent=2) + "\n")
    print(f"Wrote OCIDs only: {map_path} and {json_path}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    template = commands.add_parser("template", help="create a private blank values file")
    template.add_argument("--output", required=True)
    check = commands.add_parser("check", help="validate the values file without OCI access")
    check.add_argument("--values", required=True)
    upload_command = commands.add_parser("upload", help="preview or create secrets in OCI Vault")
    upload_command.add_argument("--values", required=True)
    upload_command.add_argument("--compartment-id", required=True)
    upload_command.add_argument("--vault-id", required=True)
    upload_command.add_argument("--region", default="us-ashburn-1")
    upload_command.add_argument("--config-file", default=str(Path.home() / ".oci" / "config"))
    upload_command.add_argument("--profile", default="DEFAULT")
    upload_command.add_argument("--output-dir", required=True)
    upload_command.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    names = expected_names()
    if args.command == "template":
        write_template(Path(args.output), names)
        return
    values = read_values(Path(args.values), names)
    if args.command == "check":
        print(f"Validated {len(values)} values without displaying them or contacting OCI.")
        return
    upload(args, names, values)


if __name__ == "__main__":
    try:
        main()
    except VaultInputError as error:
        print(f"vault-secrets: {error}", file=sys.stderr)
        sys.exit(1)
    except FileExistsError:
        print("vault-secrets: the template file already exists; it was not overwritten", file=sys.stderr)
        sys.exit(1)
    except OSError as error:
        print(f"vault-secrets: local file operation failed ({type(error).__name__})", file=sys.stderr)
        sys.exit(1)
    except Exception as error:
        # SDK errors can include request details. Never print their messages or tracebacks.
        print(f"vault-secrets: OCI operation failed ({type(error).__name__}); no values were printed", file=sys.stderr)
        sys.exit(1)
