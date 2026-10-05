import os
import pathlib
import subprocess
import tempfile
import unittest


REPO = pathlib.Path(__file__).resolve().parents[3]
SOURCE = REPO / "deploy/oci/host/deploy-release.sh"
RELEASE = "a" * 40


class DeployReleaseTests(unittest.TestCase):
    def run_scenario(self, mode="deploy", failure=""):
        root = pathlib.Path(tempfile.mkdtemp(prefix="inspection-deploy-test-"))
        for relative in (
            "etc/inspection/releases", "etc/inspection/secrets.d", "run/lock",
            "opt/inspection/releases/previous/deploy/oci", "opt/inspection/oci-cli/bin",
            "usr/local/sbin", "bin",
        ):
            (root / relative).mkdir(parents=True, exist_ok=True)
        previous = root / "opt/inspection/releases/previous/deploy/oci"
        candidate = root / "opt/inspection/releases" / RELEASE / "deploy/oci"
        candidate.mkdir(parents=True)
        (previous / "compose.yaml").write_text("name: inspection\n")
        (candidate / "compose.yaml").write_text("name: inspection\n")
        (root / "opt/inspection/current").symlink_to(previous)
        (root / "etc/inspection/public.env").write_text("ADMIN_ORIGIN=https://example.test\n")
        (root / "etc/inspection/compose.env").write_text("OLD=1\n")
        (root / "etc/inspection/releases/current.env").write_text("OLD=1\n")
        (root / f"etc/inspection/releases/{RELEASE}.env").write_text(f"RELEASE_SHA={RELEASE}\n")
        (root / "etc/inspection/secrets.d/GHCR_USERNAME").write_text("qa\n")
        (root / "etc/inspection/secrets.d/GHCR_READ_TOKEN").write_text("fake\n")

        def executable(relative, body):
            path = root / relative
            path.write_text(body)
            path.chmod(0o755)

        executable("bin/flock", "#!/bin/sh\nexit 0\n")
        executable("bin/chown", "#!/bin/sh\nexit 0\n")
        executable("bin/install", "#!/usr/bin/env python3\nimport shutil,sys\nshutil.copyfile(sys.argv[-2],sys.argv[-1])\n")
        executable("bin/mv", "#!/bin/bash\nif [[ $1 == -Tf ]]; then shift; rm -f \"$2\"; fi\nexec /bin/mv -f \"$@\"\n")
        executable("bin/docker", """#!/bin/bash
printf '%s\\n' "$*" >> "$TRACE"
if [[ "$*" == *'logout ghcr.io'* ]]; then exit 0; fi
if [[ "$FAILURE" == config && "$*" == *' config --quiet'* ]]; then exit 1; fi
if [[ "$FAILURE" == dependencies && "$*" == *'up -d --wait --wait-timeout 300 postgres'* ]]; then exit 1; fi
if [[ "$FAILURE" == migration && "$*" == *'run --rm inspection-migrate'* ]]; then exit 1; fi
if [[ "$FAILURE" == incompatible && "$*" == *'run --rm --no-deps inspection-schema-check'* ]]; then exit 1; fi
if [[ "$FAILURE" == rollback_stop && "$*" == *' stop inspection-api inspection-worker'* ]]; then exit 1; fi
exit 0
""")
        executable("usr/local/sbin/inspection-secrets-refresh", "#!/bin/sh\nexit 0\n")
        executable("usr/local/sbin/inspection-smoke", "#!/bin/sh\n[ \"$FAILURE\" != smoke ]\n")

        script = SOURCE.read_text()
        for prefix in ("/etc/inspection", "/opt/inspection", "/usr/local/sbin", "/run/"):
            script = script.replace(prefix, str(root) + prefix)
        target = root / "deploy-release.sh"
        target.write_text(script)
        env = {
            **os.environ,
            "PATH": f"{root}/bin:{os.environ['PATH']}",
            "TRACE": str(root / "trace.log"),
            "FAILURE": failure,
        }
        args = ["bash", str(target), RELEASE]
        if mode == "rollback":
            args.append("--rollback")
        result = subprocess.run(args, env=env, text=True, capture_output=True)
        trace = (root / "trace.log").read_text().splitlines()
        return root, result, trace

    def test_preflight_failure_preserves_previous_release(self):
        root, result, trace = self.run_scenario(failure="config")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(" stop " in f" {line} " for line in trace))
        self.assertIn("/previous/", str((root / "opt/inspection/current").resolve()))

    def test_dependency_failure_blocks_and_never_restarts_old_application(self):
        root, result, trace = self.run_scenario(failure="dependencies")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((root / "etc/inspection/deployment-blocked").exists())
        self.assertTrue(any("stop inspection-api inspection-worker" in line for line in trace))
        self.assertFalse(any("--no-deps --wait" in line for line in trace))

    def test_migration_failure_blocks_without_automatic_rollback(self):
        root, result, trace = self.run_scenario(failure="migration")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((root / "etc/inspection/deployment-blocked").exists())
        self.assertFalse(any("previous/deploy/oci/compose.yaml up" in line for line in trace))

    def test_smoke_failure_after_migration_does_not_restart_old_release(self):
        root, result, trace = self.run_scenario(failure="smoke")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any("run --rm inspection-migrate" in line for line in trace))
        self.assertTrue((root / "etc/inspection/deployment-blocked").exists())
        self.assertFalse(any("previous/deploy/oci/compose.yaml up" in line for line in trace))

    def test_incompatible_rollback_stops_before_mutation_or_migration(self):
        root, result, trace = self.run_scenario(mode="rollback", failure="incompatible")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any("inspection-schema-check" in line for line in trace))
        self.assertFalse(any("inspection-migrate" in line or " stop " in f" {line} " for line in trace))

    def test_failed_rollback_stop_does_not_restart_the_previous_application(self):
        root, result, trace = self.run_scenario(mode="rollback", failure="rollback_stop")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((root / "etc/inspection/deployment-blocked").exists())
        self.assertFalse(any("previous/deploy/oci/compose.yaml up" in line for line in trace))

    def test_compatible_rollback_never_runs_migrations(self):
        root, result, trace = self.run_scenario(mode="rollback")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any("inspection-schema-check" in line for line in trace))
        self.assertFalse(any("inspection-migrate" in line or "inspection-prompt-seed" in line for line in trace))
        self.assertFalse((root / "etc/inspection/deployment-blocked").exists())
        self.assertEqual((root / "etc/inspection/releases/previous.env").read_text(), "OLD=1\n")


if __name__ == "__main__":
    unittest.main()
