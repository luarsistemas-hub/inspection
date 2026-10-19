import hashlib
import json
import os
import pathlib
import re
import subprocess
import tempfile
import unittest
import importlib.util


SOURCE = pathlib.Path(__file__).resolve().parents[1] / "host/apply-plan.sh"
RELEASE = "a" * 40
PLANNER_SPEC = importlib.util.spec_from_file_location("apply_planner", SOURCE.parents[1] / "planner.py")
PLANNER = importlib.util.module_from_spec(PLANNER_SPEC)
PLANNER_SPEC.loader.exec_module(PLANNER)


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class ApplyPlanTests(unittest.TestCase):
    def scenario(self, failure="services", prior_block="", migration=False, schema_version=2, operation="deploy", targets=None, video_active=False, destination_no_video=False, runtime_mismatch=False, missing_runtime=False, first_install=False, bad_recovery_ref=False, real_smoke=False, through_runner=False):
        temporary = tempfile.TemporaryDirectory(prefix="inspection-apply-test-")
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        for relative in ("etc/inspection/releases", "etc/inspection/secrets.d", "run/lock",
                         "opt/inspection/releases/previous/deploy/oci", "usr/local/sbin", "bin"):
            (root / relative).mkdir(parents=True)
        previous = root / "opt/inspection/releases/previous/deploy/oci"
        candidate = root / f"opt/inspection/releases/{RELEASE}/deploy/oci"
        candidate.mkdir(parents=True)
        (previous.parents[1] / "deploy/keycloak").mkdir(parents=True)
        (candidate.parents[1] / "deploy/keycloak").mkdir(parents=True)
        for directory in (previous, candidate):
            (directory / "compose.yaml").write_text("name: inspection\n")
            (directory / "Caddyfile").write_text("localhost {}\n")
            (directory / "components.json").write_bytes((SOURCE.parents[1] / "components.json").read_bytes())
            (directory / "planner.py").write_bytes((SOURCE.parents[1] / "planner.py").read_bytes())
            (directory / "litellm-config.yaml").write_text("model_list: []\n")
            (directory / "keycloak-realm.json").write_text("{}\n")
        (candidate / "host").mkdir()
        (candidate / "host/component-catalog.sh").write_bytes((SOURCE.parent / "component-catalog.sh").read_bytes())
        (candidate / "host/install-host-tools.sh").write_bytes((SOURCE.parent / "install-host-tools.sh").read_bytes())
        (candidate / "host/prepare-upgrade.sh").write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$PREPARE_TRACE"\n[ "$FAILURE" != video-preflight ]\n')
        (candidate / "host/prepare-upgrade.sh").chmod(0o755)
        (candidate / "host/publish-metrics.py").write_text('#!/usr/bin/env python3\n# candidate metrics collector\n')
        if real_smoke:
            smoke_script = (SOURCE.parent / "smoke.sh").read_text().replace("/etc/inspection", str(root / "etc/inspection"))
            (candidate / "host/smoke.sh").write_text(smoke_script)
        else:
            (candidate / "host/smoke.sh").write_text('#!/bin/bash\n[ "$FAILURE" != smoke ]\n')
        (candidate / "host/smoke.sh").chmod(0o755)
        runtime_hash = PLANNER.runtime_config_sha256(candidate.parents[1])
        (root / "opt/inspection/current").symlink_to(previous)
        images = {name.upper().replace("-", "_") + "_IMAGE": "example.test/" + name + "@sha256:" + "b" * 64
                  for name in ("api", "worker", "scheduler", "video-worker", "operations", "admin", "dashboard",
                               "capture", "onboarding", "keycloak", "postgres", "dragonfly", "rabbitmq", "caddy", "litellm")}
        old_images = {key: value for key, value in images.items() if key not in {"LITELLM_IMAGE", "VIDEO_WORKER_IMAGE"}}
        if video_active:
            old_images.update({key: images[key] for key in ("LITELLM_IMAGE", "VIDEO_WORKER_IMAGE")})
        receipt = "DEPLOYMENT_ID=previous\nSOURCE_SHA=" + "f" * 40 + "\n" + ("" if missing_runtime else "RUNTIME_CONFIG_SHA256=" + runtime_hash + "\n") + "".join(f"{k}={v}\n" for k, v in old_images.items())
        if first_install:
            (root / "etc/inspection/compose.env").unlink(missing_ok=True)
            (root / "etc/inspection/releases/current.env").unlink(missing_ok=True)
            old_images = {}
        else:
            (root / "etc/inspection/compose.env").write_text(receipt)
            (root / "etc/inspection/releases/current.env").write_text(receipt)
        (root / "etc/inspection/public.env").write_text("# test fixture\n")
        block = root / "etc/inspection/deployment-blocked"
        if prior_block:
            block.write_text(prior_block)
        for name in ("GHCR_USERNAME", "GHCR_READ_TOKEN"):
            (root / f"etc/inspection/secrets.d/{name}").write_text("fixture\n")
        (root / "etc/inspection/secrets.d/INSPECTION_METRICS_TOKEN").write_text("metrics-fixture\n")
        (root / "etc/inspection/public.env").write_text(
            "SMTP_ADDRESS=smtp.example.test:587\nSMTP_FROM=inspection@example.test\n" + "".join(
                f"{key}=https://example.test\n" for key in ("ADMIN_ORIGIN", "DASHBOARD_ORIGIN", "CAPTURE_ORIGIN", "ONBOARDING_ORIGIN", "API_ORIGIN", "OIDC_ISSUER", "STORAGE_ORIGIN")) + "OCI_S3_BUCKET=inspection\n")
        (root / "etc/inspection/secrets.d/SMTP_USERNAME").write_text("smtp-user")
        (root / "etc/inspection/secrets.d/SMTP_PASSWORD").write_text("smtp-password")
        targets = targets or (list(PLANNER.COMPONENTS) if operation == "recovery" else ["api", "worker", "scheduler", "video-worker", "operations"])
        recovery_ref = {"deploymentId":"blocked-1", "planSha256":"c"*64}
        if operation == "recovery" and not prior_block:
            prior_block = "DEPLOYMENT_ID=blocked-1\nPLAN_SHA256=" + "c"*64 + "\nPHASE=services\nREASON=failed\n"
            block.write_text(prior_block)
        if bad_recovery_ref:
            prior_block = "DEPLOYMENT_ID=other-block\nPLAN_SHA256=" + "d"*64 + "\nPHASE=migration\n"
            block.write_text(prior_block)
        plan = {"schemaVersion":schema_version, "operation":operation, "deploymentId": "deploy-test-1", "sourceSha": RELEASE,
                "targets": targets, "desiredImages": images, "runtimeConfigSha256":runtime_hash,
                **({"recoveryOf":recovery_ref} if operation == "recovery" else {}),
                "componentSources": {name:"b"*40 for name in ("api","worker","scheduler","video-worker","operations","admin","dashboard","capture","onboarding","keycloak")},
                "expectedActiveStateSha256": digest(old_images), "operations": {"runMigration": migration or operation == "recovery"}}
        if operation == "rollback":
            plan["rollbackTargetHasVideoWorker"] = not destination_no_video
        if runtime_mismatch:
            plan["runtimeConfigSha256"] = "e"*64
        plan["planSha256"] = digest(plan)
        plan_path = root / "plan.json"
        plan_path.write_text(json.dumps(plan))

        def executable(name, body):
            target = root / name
            target.write_text(body)
            target.chmod(0o755)

        executable("bin/id", "#!/bin/sh\necho 0\n")
        executable("bin/chown", "#!/bin/sh\nexit 0\n")
        executable("bin/flock", "#!/bin/sh\nexit 0\n")
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
if [[ $REAL_SMOKE == 1 && $* == *'config --services'* ]]; then printf '%s\\n' inspection-api inspection-worker inspection-scheduler inspection-video-worker inspection-operations admin dashboard capture onboarding keycloak caddy litellm; exit 0; fi
if [[ $* == *'config --services'* ]]; then printf '%s\\n' inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding keycloak caddy litellm; exit 0; fi
if [[ $REAL_SMOKE == 1 && $* == *'config --environment'* ]]; then
  for image in API_IMAGE WORKER_IMAGE SCHEDULER_IMAGE VIDEO_WORKER_IMAGE OPERATIONS_IMAGE ADMIN_IMAGE DASHBOARD_IMAGE CAPTURE_IMAGE ONBOARDING_IMAGE KEYCLOAK_IMAGE; do echo "$image=example.test/image@sha256:bbbb"; done
  exit 0
fi
if [[ $* == *'ps -q '* ]]; then echo container1; exit 0; fi
if [[ $* == *'exec -T inspection-video-worker '* ]]; then exit 0; fi
if [[ $* == inspect\\ --format* ]]; then
  if [[ $REAL_SMOKE == 1 && $* == *'{{if .State.Health}}'* ]]; then echo healthy; elif [[ $REAL_SMOKE == 1 && $* == *'{{.State.Status}}'* ]]; then echo running; else echo image-id; fi
  exit 0
fi
if [[ $* == *'image inspect --format'* ]]; then
  if [[ $FAILURE == digest ]]; then echo 'wrong@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'; else
    for service in api worker scheduler video-worker operations admin dashboard capture onboarding keycloak; do echo "example.test/$service@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"; done
    echo 'docker.io/example/caddy_image@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
  fi
  exit 0
fi
if [[ $* == *'image inspect '* ]]; then exit 0; fi
if [[ $* == *'logs --no-color'* ]]; then echo 'worker: configuration rejected'; exit 0; fi
if [[ $* == *'ps -a --format json'* ]]; then echo '{"Service":"inspection-scheduler","Health":"unhealthy"}'; exit 0; fi
if [[ $FAILURE == preflight && $* == *'config --quiet'* ]]; then exit 1; fi
if [[ $FAILURE == migration && $* == *'run --rm --no-deps inspection-migrate'* ]]; then exit 1; fi
if [[ $FAILURE == schema && $* == *'run --rm --no-deps inspection-schema-check'* ]]; then exit 1; fi
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
        if real_smoke:
            executable("bin/curl", """#!/bin/bash
if [[ $* == *'%{http_code}'* ]]; then echo 403; exit 0; fi
if [[ $* == *'/metrics'* ]]; then echo 'inspection_video_queue_depth 0'; exit 0; fi
if [[ $* == *'--dump-header'* ]]; then printf 'HTTP/1.1 404 Not Found\\r\\nAccess-Control-Allow-Origin: https://example.test\\r\\nAccess-Control-Expose-Headers: ETag\\r\\n\\r\\n'; fi
exit 0
""")
        executable("usr/local/sbin/inspection-smoke", "#!/bin/sh\n[ \"$FAILURE\" != smoke ]\n")
        env = {**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}",
               "TRACE": str(root / "trace"), "FAILURE": failure,
               "PREPARE_TRACE": str(root / "prepare-trace"),
               "REAL_SMOKE": "1" if real_smoke else "0",
               "INSPECTION_APPROVED_PLAN_SHA256": plan["planSha256"]}
        if through_runner:
            # stage-and-apply's archive extractor writes every regular file as 0600.
            release_apply = SOURCE.read_text()
            for prefix in ("/etc/inspection", "/opt/inspection", "/usr/local/sbin", "/var/lib/inspection-deploy", "/run/"):
                release_apply = release_apply.replace(prefix, str(root) + prefix)
            (candidate / "host/apply-plan.sh").write_text(release_apply)
            for path in candidate.parents[1].rglob("*"):
                if path.is_file():
                    path.chmod(0o600)
            self.assertTrue(all(path.stat().st_mode & 0o777 == 0o600
                                for path in candidate.parents[1].rglob("*") if path.is_file()))
            job_dir = root / "var/lib/inspection-deploy/jobs/deploy-test-1"
            job_dir.mkdir(parents=True)
            (job_dir / "plan.json").write_text(json.dumps(plan))
            runner = (SOURCE.parent / "run-deployment.sh").read_text()
            for prefix in ("/etc/inspection", "/opt/inspection", "/usr/local/sbin", "/var/lib/inspection-deploy", "/run/"):
                runner = runner.replace(prefix, str(root) + prefix)
            target = root / "run-deployment.sh"
            target.write_text(runner)
            result = subprocess.run(["bash", str(target), "deploy-test-1", RELEASE, plan["planSha256"]],
                                    env=env, capture_output=True, text=True)
        else:
            script = SOURCE.read_text()
            for prefix in ("/etc/inspection", "/opt/inspection", "/usr/local/sbin", "/var/lib/inspection-deploy", "/run/"):
                script = script.replace(prefix, str(root) + prefix)
            target = root / "apply-plan.sh"
            target.write_text(script)
            result = subprocess.run(["bash", str(target), str(plan_path), str(candidate.parents[1])],
                                    env=env, capture_output=True, text=True)
        trace = (root / "trace").read_text().splitlines() if (root / "trace").exists() else []
        return root, result, trace, block

    def test_failure_after_go_migration_keeps_block_and_diagnostics(self):
        root, result, trace, block = self.scenario()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("after database migration started", block.read_text())
        self.assertIn("DEPLOYMENT_ID=previous", (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn("/previous/", str((root / "opt/inspection/current").resolve()))
        diagnostics = root / "var/lib/inspection-deploy/jobs/deploy-test-1/diagnostics"
        self.assertIn("configuration rejected", (diagnostics / "containers.log").read_text())
        self.assertEqual((diagnostics / "containers.log").stat().st_mode & 0o777, 0o600)
        self.assertEqual(diagnostics.stat().st_mode & 0o777, 0o700)

    def test_video_host_preparation_runs_before_any_service_stop(self):
        root, result, trace, _ = self.scenario()
        self.assertEqual(result.returncode, 1)
        self.assertTrue((root / "prepare-trace").is_file())
        self.assertLess(trace.index(next(line for line in trace if "image inspect" in line)), trace.index(next(line for line in trace if " stop " in line)))

    def test_video_host_preparation_failure_keeps_services_running(self):
        _, result, trace, _ = self.scenario(failure="video-preflight")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(" stop " in line for line in trace))
        self.assertFalse(any("up -d --no-deps --wait" in line for line in trace))

    def test_promotion_installs_release_metrics_collector(self):
        root, result, _, _ = self.scenario(failure="none")
        self.assertEqual(result.returncode, 0, result.stderr)
        installed = root / "usr/local/sbin/inspection-publish-metrics"
        self.assertIn("candidate metrics collector", installed.read_text())
        unit = (SOURCE.parent / "install-host.sh").read_text()
        self.assertIn("ExecStart=/usr/local/sbin/inspection-publish-metrics", unit)

    def test_deploy_with_block_is_rejected_without_modification(self):
        existing = "DEPLOYMENT_ID=blocked-1\nPLAN_SHA256=" + "c"*64 + "\nPHASE=migration\nREASON=Earlier migration failure\n"
        _, result, trace, block = self.scenario(prior_block=existing)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("only a matching recovery", result.stderr)
        self.assertEqual(block.read_text(), existing)
        self.assertFalse(any(" stop " in line or " up " in line for line in trace))

    def test_gateway_is_started_before_worker_and_stopped_on_failure(self):
        _, result, trace, block = self.scenario(failure="services")
        self.assertEqual(result.returncode, 1, result.stderr)
        start = next(i for i, line in enumerate(trace) if "up -d --no-deps --wait --wait-timeout 300 litellm" in line)
        worker = next(i for i, line in enumerate(trace) if "up -d --no-deps --wait --wait-timeout 300 inspection-api inspection-worker" in line)
        self.assertLess(start, worker)
        self.assertTrue(block.exists())
        self.assertTrue(any("inspection-migrate" in line for line in trace))

    def test_failed_recovery_keeps_block(self):
        _, result, _, block = self.scenario(failure="recovery", operation="recovery")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("DEPLOYMENT_ID=blocked-1", block.read_text())
        self.assertIn("PLAN_SHA256=" + "c"*64, block.read_text())

    def test_smoke_failure_recovers_without_migration(self):
        root, result, trace, block = self.scenario(failure="smoke", targets=["admin"])
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertFalse(block.exists())
        self.assertTrue(any("/current/compose.yaml up" in line for line in trace))
        self.assertFalse(any("inspection-migrate" in line for line in trace))
        self.assertIn("DEPLOYMENT_ID=previous", (root / "etc/inspection/releases/current.env").read_text())
        diagnostics = root / "var/lib/inspection-deploy/jobs/deploy-test-1/diagnostics"
        self.assertTrue(diagnostics.is_dir())

    def test_migration_or_promotion_failure_never_automatically_recovers(self):
        for failure, migration, reason in (("migration", True, "database migration started"),
                                           ("services", True, "database migration started"),
                                           ("smoke", True, "database migration started"),
                                           ("promoting", False, "promoting state")):
            with self.subTest(failure=failure):
                _, result, trace, block = self.scenario(failure=failure, migration=migration, targets=["admin"] if failure == "promoting" else None)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertIn(reason, block.read_text())
                self.assertFalse(any("/current/compose.yaml up" in line for line in trace))

    def test_preflight_does_not_mutate_services_or_existing_block(self):
        _, result, trace, block = self.scenario(failure="preflight")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertFalse(block.exists())
        self.assertFalse(any(" stop " in line or " up " in line for line in trace))

    def test_schema_v1_plan_is_rejected_with_regeneration_message(self):
        _, result, _, _ = self.scenario(failure="", schema_version=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("regenerate the plan", result.stderr)

    def test_success_promotes_receipt_and_clears_block(self):
        root, result, trace, block = self.scenario(failure="")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(block.exists())
        env = (root / "etc/inspection/compose.env").read_text()
        self.assertIn("DEPLOYMENT_ID=deploy-test-1", (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn("VIDEO_WORKER_IMAGE=" + "example.test/video-worker@sha256:" + "b" * 64, env)
        self.assertIn("COMPONENT_SOURCE_VIDEO_WORKER=" + "b" * 40, (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn(RELEASE, str((root / "opt/inspection/current").resolve()))
        smtp = next(line for line in env.splitlines() if line.startswith("KEYCLOAK_SMTP_SERVER_JSON="))
        self.assertEqual(json.loads(smtp.split("=", 1)[1].strip("'")), {
            "host": "smtp.example.test", "port": "587", "from": "inspection@example.test",
            "auth": "true", "user": "smtp-user", "password": "smtp-password",
            "ssl": "false", "starttls": "true",
        })

    def test_apply_plan_runs_release_tree_smoke_and_promotes(self):
        root, result, trace, block = self.scenario(failure="", real_smoke=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(block.exists())
        self.assertIn("config --services", "\n".join(trace))
        self.assertIn("DEPLOYMENT_ID=deploy-test-1", (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn(RELEASE, str((root / "opt/inspection/current").resolve()))

    def test_mode_0600_release_runs_end_to_end_through_supervised_runner(self):
        root, result, trace, block = self.scenario(failure="", real_smoke=True, through_runner=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((root / "prepare-trace").is_file())
        self.assertFalse(block.exists())
        self.assertIn("DEPLOYMENT_ID=deploy-test-1", (root / "etc/inspection/releases/current.env").read_text())
        self.assertIn(RELEASE, str((root / "opt/inspection/current").resolve()))

    def test_video_worker_absent_from_old_compose_does_not_break_stop(self):
        _, result, trace, _ = self.scenario(failure="", targets=["api", "worker", "scheduler", "video-worker", "operations"])
        self.assertEqual(result.returncode, 0, result.stderr)
        stop = next(line for line in trace if " stop " in line)
        self.assertNotIn("inspection-video-worker", stop)

    def test_digest_mismatch_prevents_promotion(self):
        root, result, trace, block = self.scenario(failure="digest")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("digest differs", result.stderr)
        self.assertTrue(block.exists())
        self.assertIn("DEPLOYMENT_ID=previous", (root / "etc/inspection/releases/current.env").read_text())

    def test_recovery_success_clears_block_only_after_all_checks(self):
        _, result, trace, block = self.scenario(failure="", operation="recovery")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(block.exists())
        self.assertTrue(any("inspection-migrate" in line for line in trace))
        self.assertTrue(any("inspection-schema-check" in line for line in trace))

    def test_recovery_reference_must_match_active_block(self):
        _, result, trace, block = self.scenario(failure="", operation="recovery", bad_recovery_ref=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Recovery reference does not match", result.stderr)
        self.assertIn("DEPLOYMENT_ID=other-block", block.read_text())
        self.assertFalse(trace)

    def test_recovery_preflight_failure_updates_phase_without_changing_reference(self):
        _, result, _, block = self.scenario(failure="preflight", operation="recovery")
        self.assertNotEqual(result.returncode, 0)
        content=block.read_text()
        self.assertIn("DEPLOYMENT_ID=blocked-1", content)
        self.assertIn("PLAN_SHA256=" + "c"*64, content)
        self.assertIn("PHASE=preflight", content)
        self.assertIn("Recovery failed before migrations", content)

    def test_first_install_waits_for_dependencies_before_migration(self):
        _, result, trace, _ = self.scenario(failure="", first_install=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        dependencies = next(i for i,line in enumerate(trace) if "up -d --wait --wait-timeout 300 postgres rabbitmq dragonfly keycloak litellm" in line)
        migration = next(i for i,line in enumerate(trace) if "inspection-migrate" in line)
        self.assertLess(dependencies, migration)

    def test_rollback_schema_hash_and_legacy_receipt_fail_before_stop(self):
        cases = (
            ("schema", {"operation":"rollback", "video_active":True}, "target images do not support the current database schema"),
            ("", {"operation":"rollback", "video_active":True, "destination_no_video":True}, "target has no video-worker"),
            ("", {"operation":"rollback", "video_active":True, "runtime_mismatch":True}, "Runtime configuration hash does not match"),
            ("", {"operation":"rollback", "video_active":True, "missing_runtime":True}, "runtime configuration does not match"),
        )
        for failure, options, message in cases:
            with self.subTest(options=options):
                _, result, trace, _ = self.scenario(failure=failure, **options)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertFalse(any(" stop " in line for line in trace))

    def test_executor_paths_are_versioned_after_stage_lock_release(self):
        runner = (SOURCE.parent / "run-deployment.sh").read_text()
        stage = (SOURCE.parent / "stage-and-apply.sh").read_text()
        self.assertIn('"$release_dir/deploy/oci/host/apply-plan.sh"', runner)
        self.assertIn('"/opt/inspection/host-tools/$source_sha/run-deployment.sh"', stage)
        self.assertNotIn("/usr/local/sbin/inspection-apply-plan", runner)

    def test_release_scripts_are_never_invoked_directly(self):
        scripts = list(SOURCE.parent.glob("*.sh")) + [SOURCE.parents[1] / "ops.sh"]
        violations = []
        for path in scripts:
            for number, line in enumerate(path.read_text().splitlines(), 1):
                for extension, interpreter in (("sh", "bash"), ("py", "python3")):
                    pattern = rf'"?\$release_dir/[^" ]+\.{extension}"?'
                    for match in re.finditer(pattern, line):
                        prefix = line[:match.start()].rstrip()
                        if (prefix.endswith("=") or "install " in prefix or "[[" in prefix
                                or prefix.endswith(interpreter)
                                or (extension == "sh" and prefix.endswith("source"))):
                            continue
                        violations.append(f"{path.name}:{number}: {line.strip()}")
        self.assertFalse(violations, "release-tree scripts must use an explicit interpreter:\n" + "\n".join(violations))


if __name__ == "__main__":
    unittest.main()
