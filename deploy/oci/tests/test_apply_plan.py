import hashlib
import json
import os
import pathlib
import subprocess
import tempfile
import unittest


SOURCE = pathlib.Path(__file__).resolve().parents[1] / "host/apply-plan.sh"
RELEASE = "a" * 40


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class ApplyPlanTests(unittest.TestCase):
    def scenario(self, failure="services", prior_block="", migration=False):
        temporary = tempfile.TemporaryDirectory(prefix="inspection-apply-test-")
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        for relative in ("etc/inspection/releases", "etc/inspection/secrets.d", "run",
                         "opt/inspection/releases/previous/deploy/oci", "usr/local/sbin", "bin"):
            (root / relative).mkdir(parents=True)
        previous = root / "opt/inspection/releases/previous/deploy/oci"
        candidate = root / f"opt/inspection/releases/{RELEASE}/deploy/oci"
        candidate.mkdir(parents=True)
        for directory in (previous, candidate):
            (directory / "compose.yaml").write_text("name: inspection\n")
            (directory / "Caddyfile").write_text("localhost {}\n")
        (root / "opt/inspection/current").symlink_to(previous)
        images = {name.upper() + "_IMAGE": "example.test/" + name + "@sha256:" + "b" * 64
                  for name in ("api", "worker", "scheduler", "operations", "admin", "dashboard",
                               "capture", "onboarding", "keycloak", "postgres", "dragonfly", "rabbitmq", "caddy", "litellm")}
        old_images = {key: value for key, value in images.items() if key != "LITELLM_IMAGE"}
        receipt = "DEPLOYMENT_ID=previous\n" + "".join(f"{k}={v}\n" for k, v in old_images.items())
        (root / "etc/inspection/compose.env").write_text(receipt)
        (root / "etc/inspection/releases/current.env").write_text(receipt)
        (root / "etc/inspection/public.env").write_text("# test fixture\n")
        block = root / "etc/inspection/deployment-blocked"
        if prior_block:
            block.write_text(prior_block)
        for name in ("GHCR_USERNAME", "GHCR_READ_TOKEN"):
            (root / f"etc/inspection/secrets.d/{name}").write_text("fixture\n")
        (root / "etc/inspection/public.env").write_text(
            "SMTP_ADDRESS=smtp.example.test:587\nSMTP_FROM=inspection@example.test\n")
        (root / "etc/inspection/secrets.d/SMTP_USERNAME").write_text("smtp-user")
        (root / "etc/inspection/secrets.d/SMTP_PASSWORD").write_text("smtp-password")
        plan = {"deploymentId": "deploy-test-1", "sourceSha": RELEASE,
                "targets": ["worker"], "desiredImages": images,
                "expectedActiveStateSha256": digest(old_images), "operations": {"runMigration": migration}}
        plan["planSha256"] = digest(plan)
        plan_path = root / "plan.json"
        plan_path.write_text(json.dumps(plan))

        def executable(name, body):
            target = root / name
            target.write_text(body)
            target.chmod(0o755)

        executable("bin/id", "#!/bin/sh\necho 0\n")
        executable("bin/chown", "#!/bin/sh\nexit 0\n")
        executable("bin/install", """#!/usr/bin/env python3
import os,pathlib,shutil,sys
args=sys.argv[1:]
if os.environ['FAILURE']=='promoting' and args[-1].endswith('/.compose.env.new'): sys.exit(1)
if '-d' in args: pathlib.Path(args[-1]).mkdir(parents=True,exist_ok=True)
else: shutil.copyfile(args[-2],args[-1])
if '-m' in args: os.chmod(args[-1],int(args[args.index('-m')+1],8))
""")
        executable("bin/mv", """#!/bin/bash
if [[ $1 == -Tf ]]; then shift; rm -f "$2"; fi
exec /bin/mv -f "$@"
""")
        executable("bin/docker", """#!/bin/bash
printf '%s\\n' "$*" >> "$TRACE"
if [[ $* == *'logs --no-color'* ]]; then echo 'worker: configuration rejected'; exit 0; fi
if [[ $* == *'ps -a --format json'* ]]; then echo '{"Service":"inspection-scheduler","Health":"unhealthy"}'; exit 0; fi
if [[ $FAILURE == preflight && $* == *'config --quiet'* ]]; then exit 1; fi
if [[ $FAILURE == migration && $* == *'run --rm --no-deps inspection-migrate'* ]]; then exit 1; fi
if [[ $* == *'up -d --no-deps'* ]]; then
  if [[ $* == *'/current/compose.yaml'* ]]; then
    [[ $FAILURE != recovery ]]; exit $?
  fi
  if [[ $* == *' litellm' ]]; then
    [[ $FAILURE != gateway ]]; exit $?
  fi
  [[ $FAILURE != services && $FAILURE != recovery ]]; exit $?
fi
exit 0
""")
        executable("usr/local/sbin/inspection-smoke", "#!/bin/sh\n[ \"$FAILURE\" != smoke ]\n")
        script = SOURCE.read_text()
        for prefix in ("/etc/inspection", "/opt/inspection", "/usr/local/sbin", "/var/lib/inspection-deploy", "/run/"):
            script = script.replace(prefix, str(root) + prefix)
        target = root / "apply-plan.sh"
        target.write_text(script)
        result = subprocess.run(["bash", str(target), str(plan_path), str(candidate.parents[1])],
                                env={**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}",
                                     "TRACE": str(root / "trace"), "FAILURE": failure,
                                     "INSPECTION_APPROVED_PLAN_SHA256": plan["planSha256"]},
                                capture_output=True, text=True)
        trace = (root / "trace").read_text().splitlines() if (root / "trace").exists() else []
        return root, result, trace, block

    def test_recovered_services_clear_new_block_but_deployment_still_fails(self):
        root, result, trace, block = self.scenario()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("Previous services recovered", result.stderr)
        self.assertFalse(block.exists())
        self.assertIn("DEPLOYMENT_ID=previous", (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn("/previous/", str((root / "opt/inspection/current").resolve()))
        self.assertEqual(list((root / "run").iterdir()), [])
        diagnostics = root / "var/lib/inspection-deploy/jobs/deploy-test-1/diagnostics"
        self.assertIn("configuration rejected", (diagnostics / "containers.log").read_text())
        self.assertEqual((diagnostics / "containers.log").stat().st_mode & 0o777, 0o600)
        self.assertEqual(diagnostics.stat().st_mode & 0o777, 0o700)
        logs = next(i for i, line in enumerate(trace) if "logs --no-color" in line)
        recovery = next(i for i, line in enumerate(trace) if "/current/compose.yaml up" in line)
        self.assertLess(logs, recovery)

    def test_recovery_preserves_preexisting_block(self):
        _, result, _, block = self.scenario(prior_block="REASON=Earlier migration failure\n")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(block.read_text(), "REASON=Earlier migration failure\n")

    def test_gateway_is_started_before_worker_and_stopped_on_failure(self):
        _, result, trace, block = self.scenario(failure="services")
        self.assertEqual(result.returncode, 1, result.stderr)
        start = next(i for i, line in enumerate(trace) if "up -d --no-deps --wait --wait-timeout 300 litellm" in line)
        worker = next(i for i, line in enumerate(trace) if "up -d --no-deps --wait --wait-timeout 300 inspection-worker" in line)
        self.assertLess(start, worker)
        self.assertTrue(any("stop litellm" in line for line in trace))
        self.assertFalse(block.exists())

    def test_failed_recovery_keeps_block(self):
        _, result, _, block = self.scenario(failure="recovery")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("Failed to recover selected services", block.read_text())

    def test_smoke_failure_recovers_without_migration(self):
        _, result, trace, block = self.scenario(failure="smoke")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertFalse(block.exists())
        self.assertTrue(any("/current/compose.yaml up" in line for line in trace))

    def test_migration_or_promotion_failure_never_automatically_recovers(self):
        for failure, migration, reason in (("migration", True, "database migration started"),
                                           ("services", True, "database migration started"),
                                           ("smoke", True, "database migration started"),
                                           ("promoting", False, "promoting state")):
            with self.subTest(failure=failure):
                _, result, trace, block = self.scenario(failure=failure, migration=migration)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertIn(reason, block.read_text())
                self.assertFalse(any("/current/compose.yaml up" in line for line in trace))

    def test_preflight_does_not_mutate_services_or_existing_block(self):
        _, result, trace, block = self.scenario(failure="preflight", prior_block="previous block\n")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(block.read_text(), "previous block\n")
        self.assertFalse(any(" stop " in line or " up " in line for line in trace))

    def test_success_promotes_receipt_and_clears_block(self):
        root, result, _, block = self.scenario(failure="", prior_block="previous block\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(block.exists())
        self.assertIn("DEPLOYMENT_ID=deploy-test-1", (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn(RELEASE, str((root / "opt/inspection/current").resolve()))
        env = (root / "etc/inspection/compose.env").read_text()
        smtp = next(line for line in env.splitlines() if line.startswith("KEYCLOAK_SMTP_SERVER_JSON="))
        self.assertEqual(json.loads(smtp.split("=", 1)[1].strip("'")), {
            "host": "smtp.example.test", "port": "587", "from": "inspection@example.test",
            "auth": "true", "user": "smtp-user", "password": "smtp-password",
            "ssl": "false", "starttls": "true",
        })


if __name__ == "__main__":
    unittest.main()
