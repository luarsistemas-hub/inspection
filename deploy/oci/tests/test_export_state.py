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
            release = root / "opt/inspection/releases" / ("e" * 40) / "deploy/oci"
            release.mkdir(parents=True)
            (release / "components.json").write_bytes((REPO / "deploy/oci/components.json").read_bytes())
            helper = release / "host/component-catalog.sh"
            helper.parent.mkdir()
            helper.write_bytes((REPO / "deploy/oci/host/component-catalog.sh").read_bytes())
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
            receipt.write_text("DEPLOYMENT_ID=deploy-test-1\nPLAN_SHA256=" + "d" * 64 + "\nSOURCE_SHA=" + "e" * 40 + "\nRUNTIME_CONFIG_SHA256=" + "f"*64 + "\n" + "".join(f"{key}={value}\n" for key, value in images.items()) + "".join(f"COMPONENT_SOURCE_{name.upper().replace('-', '_')}={value}\n" for name, value in sources.items()))
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
            tools = root / "opt/inspection/host-tools" / ("e" * 40)
            tools.mkdir(parents=True)
            target = tools / "export-state.sh"
            target.write_text(script)
            result = subprocess.run(["bash", str(target), "current"], env={**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}", "INSPECTION_RELEASES_ROOT": str(root / "opt/inspection/releases")}, text=True, capture_output=True)
            blocked = root / "etc/inspection/deployment-blocked"
            blocked.write_text("DEPLOYMENT_ID=blocked-1\nPLAN_SHA256=" + "a"*64 + "\nPHASE=migration\nREASON=line one\nline two\n")
            blocked_result = subprocess.run(["bash", str(target), "blocked"], env={**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}", "INSPECTION_RELEASES_ROOT": str(root / "opt/inspection/releases")}, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("VIDEO_WORKER_IMAGE=ghcr.io/example/inspection-video-worker@sha256:" + "a" * 64, result.stdout)
        self.assertIn("COMPONENT_SOURCE_VIDEO_WORKER=" + "c" * 40, result.stdout)
        self.assertIn("RUNTIME_CONFIG_SHA256=" + "f" * 64, result.stdout)
        self.assertEqual(blocked_result.returncode, 0, blocked_result.stderr)
        self.assertIn("BLOCKED_DEPLOYMENT_ID=blocked-1", blocked_result.stdout)
        self.assertIn("BLOCKED_PLAN_SHA256=" + "a"*64, blocked_result.stdout)
        self.assertIn("BLOCKED_PHASE=migration", blocked_result.stdout)
        self.assertNotIn("REASON", blocked_result.stdout)


if __name__ == "__main__":
    unittest.main()
