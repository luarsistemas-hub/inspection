import hashlib
import importlib.util
import os
import pathlib
import subprocess
import tarfile
import tempfile
import unittest


SOURCE = pathlib.Path(__file__).resolve().parents[1] / "verify-release.py"
SPEC = importlib.util.spec_from_file_location("verify_release", SOURCE)
verify_release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verify_release)


class VerifyReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="inspection-release-test-")
        self.old_cwd = pathlib.Path.cwd()
        self.root = pathlib.Path(self.temp.name)
        os.chdir(self.root)
        subprocess.run(["git", "init", "-q"], check=True)
        subprocess.run(["git", "config", "user.email", "test@example.invalid"], check=True)
        subprocess.run(["git", "config", "user.name", "Release Test"], check=True)
        self.files = {
            "deploy/oci/compose.yaml": b"services: {}\n",
            "deploy/oci/host/deploy-release.sh": b"#!/bin/sh\nexit 0\n",
            "deploy/keycloak/realm.json": b'{"realm":"inspection"}\n',
        }
        for name, content in self.files.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        subprocess.run(["git", "add", "."], check=True)
        subprocess.run(["git", "commit", "-qm", "release"], check=True)
        self.release = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
        self.bundle = self.root / "release.tgz"
        with tarfile.open(self.bundle, "w:gz") as archive:
            for name in self.files:
                archive.add(self.root / name, arcname=name)
        self.checksum = self.root / "release.sha256"
        digest = hashlib.sha256(self.bundle.read_bytes()).hexdigest()
        self.checksum.write_text(f"{digest}  release.tgz\n")
        self.manifest = self.root / "release.env"
        images = [
            "API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE",
            "CAPTURE_IMAGE", "ONBOARDING_IMAGE", "POSTGRES_IMAGE",
            "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE",
        ]
        content = [f"{key}=ghcr.io/test/{key.lower()}@sha256:{'a' * 64}" for key in images]
        content.extend((f"RELEASE_SHA={self.release}", f"BUNDLE_SHA256={digest}"))
        self.manifest.write_text("\n".join(content) + "\n")

    def tearDown(self):
        os.chdir(self.old_cwd)
        self.temp.cleanup()

    def test_accepts_complete_source_matched_digest_release(self):
        verify_release.verify(str(self.bundle), str(self.manifest), str(self.checksum), self.release)

    def test_rejects_bundle_content_that_differs_from_commit(self):
        with tarfile.open(self.bundle, "w:gz") as archive:
            member = tarfile.TarInfo("deploy/oci/compose.yaml")
            payload = b"services: {changed: true}\n"
            member.size = len(payload)
            import io
            archive.addfile(member, io.BytesIO(payload))
            for name in ("deploy/oci/host/deploy-release.sh", "deploy/keycloak/realm.json"):
                archive.add(self.root / name, arcname=name)
        digest = hashlib.sha256(self.bundle.read_bytes()).hexdigest()
        self.checksum.write_text(f"{digest}  release.tgz\n")
        self.manifest.write_text(self.manifest.read_text().replace(
            self.manifest.read_text().split("BUNDLE_SHA256=")[1].strip(), digest
        ))
        with self.assertRaisesRegex(ValueError, "does not match source commit"):
            verify_release.verify(str(self.bundle), str(self.manifest), str(self.checksum), self.release)

    def test_rejects_mutable_image_reference(self):
        self.manifest.write_text(self.manifest.read_text().replace(
            f"ghcr.io/test/api_image@sha256:{'a' * 64}", "ghcr.io/test/api:latest"
        ))
        with self.assertRaisesRegex(ValueError, "pinned by sha256 digest"):
            verify_release.verify(str(self.bundle), str(self.manifest), str(self.checksum), self.release)


if __name__ == "__main__":
    unittest.main()
