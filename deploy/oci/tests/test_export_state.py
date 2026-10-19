import json
import os
import pathlib
import subprocess
import tempfile
import unittest


REPO = pathlib.Path(__file__).resolve().parents[3]
SOURCE = REPO / "deploy/oci/host/export-state.sh"


class ExportStateTests(unittest.TestCase):
    def test_video_digest_and_component_sources_are_exported_from_receipt(self):
        with tempfile.TemporaryDirectory(prefix="inspection-export-state-") as directory:
            root = pathlib.Path(directory)
            release = root / "opt/inspection/releases/source/deploy/oci"
            release.mkdir(parents=True)
            (release / "components.json").write_text('{"components":{"legacy":{}}}')
            current = root / "opt/inspection/current"
            current.parent.mkdir(parents=True, exist_ok=True)
            current.symlink_to(release)
            receipts = root / "etc/inspection/releases"
            receipts.mkdir(parents=True)
            catalog = json.loads((REPO / "deploy/oci/components.json").read_text())["components"]
            images = {item["image"]: f"ghcr.io/example/inspection-{name}@sha256:{'a' * 64}" for name, item in catalog.items()}
            images.update({key: f"docker.io/example/{key.lower()}@sha256:{'b' * 64}" for key in ("POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE")})
            sources = {name: "c" * 40 for name in catalog}
            receipt = receipts / "current.env"
            receipt.write_text("DEPLOYMENT_ID=deploy-test-1\nPLAN_SHA256=" + "d" * 64 + "\nSOURCE_SHA=" + "e" * 40 + "\n" + "".join(f"{key}={value}\n" for key, value in images.items()) + "".join(f"COMPONENT_SOURCE_{name.upper().replace('-', '_')}={value}\n" for name, value in sources.items()))
            (root / "run/lock").mkdir(parents=True)
            (root / "bin").mkdir()
            id_cmd = root / "bin/id"
            id_cmd.write_text("#!/bin/sh\necho 0\n")
            id_cmd.chmod(0o755)
            flock = root / "bin/flock"
            flock.write_text("#!/bin/sh\nexit 0\n")
            flock.chmod(0o755)
            script = SOURCE.read_text()
            for prefix in ("/etc/inspection", "/opt/inspection", "/run/lock"):
                script = script.replace(prefix, str(root) + prefix)
            target = root / "export-state.sh"
            target.write_text(script)
            (root / "components.json").write_bytes((REPO / "deploy/oci/components.json").read_bytes())
            result = subprocess.run(["bash", str(target), "current"], env={**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}"}, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("VIDEO_WORKER_IMAGE=ghcr.io/example/inspection-video-worker@sha256:" + "a" * 64, result.stdout)
        self.assertIn("COMPONENT_SOURCE_VIDEO_WORKER=" + "c" * 40, result.stdout)


if __name__ == "__main__":
    unittest.main()
