import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import importlib.util


REPO = pathlib.Path(__file__).resolve().parents[3]


class DeployProtocolTests(unittest.TestCase):
    def test_catalog_is_the_ten_component_service_and_image_registry(self):
        catalog = json.loads((REPO / "deploy/oci/components.json").read_text())["components"]
        self.assertEqual(len(catalog), 10)
        self.assertEqual({item["image"] for item in catalog.values()}, {
            "API_IMAGE", "WORKER_IMAGE", "SCHEDULER_IMAGE", "VIDEO_WORKER_IMAGE", "OPERATIONS_IMAGE",
            "ADMIN_IMAGE", "DASHBOARD_IMAGE", "CAPTURE_IMAGE", "ONBOARDING_IMAGE", "KEYCLOAK_IMAGE",
        })
        self.assertIsNone(catalog["operations"]["service"])
        self.assertEqual(catalog["video-worker"]["service"], "inspection-video-worker")

    def test_component_paths_and_compatibility_prefixes_exist(self):
        catalog = json.loads((REPO / "deploy/oci/components.json").read_text())
        paths = [path for item in catalog["components"].values() for path in item["paths"]]
        paths += [path for rule in catalog["sharedRules"] for path in rule["paths"]]
        spec = importlib.util.spec_from_file_location("path_catalog_planner", REPO / "deploy/oci/planner.py")
        planner = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(planner)
        paths += list(planner.DATABASE_COMPATIBILITY_PATHS) + list(planner.CONTRACT_COMPATIBILITY_PATHS)
        for path in paths:
            if re.search(r"[*?\[]", path):
                continue
            self.assertTrue((REPO / path.rstrip("/")).exists(), f"catalog/planner path does not exist: {path}")

    @staticmethod
    def make_source_bundle(directory, installer_body="#!/bin/bash\n"):
        source = directory / "source"
        (source / "deploy/oci/host").mkdir(parents=True)
        (source / "deploy/oci/compose.yaml").write_text("services: {}\n")
        (source / "deploy/oci/host/apply-plan.sh").write_text("#!/bin/bash\n")
        (source / "deploy/oci/host/install-host-tools.sh").write_text(installer_body)
        (source / "deploy/keycloak").mkdir(parents=True)
        bundle = directory / "bundle.tgz"
        with tarfile.open(bundle, "w:gz") as archive:
            archive.add(source / "deploy/oci/compose.yaml", arcname="deploy/oci/compose.yaml")
            archive.add(source / "deploy/oci/host/apply-plan.sh", arcname="deploy/oci/host/apply-plan.sh")
            archive.add(source / "deploy/oci/host/install-host-tools.sh", arcname="deploy/oci/host/install-host-tools.sh")
            archive.add(source / "deploy/keycloak", arcname="deploy/keycloak")
        return bundle

    @staticmethod
    def make_plan(directory, bundle):
        plan = {"schemaVersion": 2, "operation": "deploy", "deploymentId": "deploy-protocol-test", "sourceSha": "a" * 40,
                "bundleSha256": hashlib.sha256(bundle.read_bytes()).hexdigest()}
        plan["planSha256"] = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        path = directory / "plan.json"
        path.write_text(json.dumps(plan))
        return path, plan

    @staticmethod
    def receiver_validator(directory):
        receiver = (REPO / "deploy/oci/host/stage-and-apply.sh").read_text()
        match = re.search(r"python3 - \"\$outer\" \"\$stage\" \"\$deployment_id\" \"\$approved_plan_sha\" <<'PY'\n(.*?)\nPY", receiver, re.S)
        if not match:
            raise AssertionError("stage-and-apply Python validator heredoc not found")
        validator = directory / "receiver-validator.py"
        validator.write_text(match.group(1) + "\n")
        return validator

    @staticmethod
    def validate_outer(validator, outer, plan):
        receiver_stage = outer.parent / (outer.stem + "-receiver-stage")
        return subprocess.run(["python3", str(validator), str(outer), str(receiver_stage), plan["deploymentId"], plan["planSha256"]], text=True, capture_output=True)

    @staticmethod
    def git_tree(repo, entries):
        content = "".join(f"{mode} {kind} {oid}\t{name}\n" for mode, kind, oid, name in entries)
        return subprocess.check_output(["git", "mktree"], cwd=repo, input=content, text=True).strip()

    def test_cli_and_publish_packages_are_accepted_by_stage_receiver(self):
        with tempfile.TemporaryDirectory(prefix="inspection-protocol-") as directory:
            tmp = pathlib.Path(directory)
            validator = self.receiver_validator(tmp)
            bundle = self.make_source_bundle(tmp)
            plan_path, plan = self.make_plan(tmp, bundle)

            fake_bin = tmp / "bin"
            fake_bin.mkdir()
            captured = tmp / "cli-upload.tar.gz"
            ssh = fake_bin / "ssh"
            ssh.write_text("#!/bin/bash\nlast=\"${!#}\"\ncase \"$last\" in apply-plan*) cat > \"$CAPTURE_UPLOAD\" ;; deployment-status*) printf '{\\\"status\\\":\\\"succeeded\\\"}\\n' ;; esac\n")
            ssh.chmod(0o755)
            result = subprocess.run(["bash", str(REPO / "deploy/oci/ops.sh"), "apply", str(plan_path), str(bundle)], cwd=REPO,
                                    env={**os.environ, "PATH": f"{fake_bin}:{os.environ['PATH']}", "INSPECTION_RUNTIME_HOST": "qa.invalid", "INSPECTION_APPROVED_PLAN_SHA256": plan["planSha256"], "CAPTURE_UPLOAD": str(captured)}, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            cli_validation = self.validate_outer(validator, captured, plan)
            with tarfile.open(captured, "r:gz") as archive:
                captured_members = [member.name for member in archive.getmembers()]
            self.assertEqual(cli_validation.returncode, 0, f"{cli_validation.stderr}; members={captured_members}")

            # Reproduce publish.yml's git archive, upload/bundle.tgz and outer package commands.
            git_repo = tmp / "workflow-repo"
            (git_repo / "deploy/oci/host").mkdir(parents=True)
            (git_repo / "deploy/oci/compose.yaml").write_text("services: {}\n")
            (git_repo / "deploy/oci/host/apply-plan.sh").write_text("#!/bin/bash\n")
            (git_repo / "deploy/oci/host/install-host-tools.sh").write_text("#!/bin/bash\n")
            (git_repo / "deploy/keycloak").mkdir(parents=True)
            subprocess.run(["git", "init", "-q"], cwd=git_repo, check=True)
            apply_oid = subprocess.check_output(["git", "hash-object", "-w", "deploy/oci/host/apply-plan.sh"], cwd=git_repo, text=True).strip()
            installer_oid = subprocess.check_output(["git", "hash-object", "-w", "deploy/oci/host/install-host-tools.sh"], cwd=git_repo, text=True).strip()
            compose_oid = subprocess.check_output(["git", "hash-object", "-w", "deploy/oci/compose.yaml"], cwd=git_repo, text=True).strip()
            host_tree = self.git_tree(git_repo, [("100644", "blob", apply_oid, "apply-plan.sh"), ("100644", "blob", installer_oid, "install-host-tools.sh")])
            oci_tree = self.git_tree(git_repo, [("100644", "blob", compose_oid, "compose.yaml"), ("040000", "tree", host_tree, "host")])
            keycloak_tree = self.git_tree(git_repo, [])
            deploy_tree = self.git_tree(git_repo, [("040000", "tree", oci_tree, "oci"), ("040000", "tree", keycloak_tree, "keycloak")])
            root_tree = self.git_tree(git_repo, [("040000", "tree", deploy_tree, "deploy")])
            source_sha = subprocess.check_output(["git", "commit-tree", root_tree, "-m", "bundle"], cwd=git_repo, text=True,
                                                 env={**os.environ, "GIT_AUTHOR_NAME": "Protocol Test", "GIT_AUTHOR_EMAIL": "test@example.invalid", "GIT_COMMITTER_NAME": "Protocol Test", "GIT_COMMITTER_EMAIL": "test@example.invalid"}).strip()
            upload = tmp / "upload"
            package = tmp / "package"
            upload.mkdir(); package.mkdir()
            subprocess.run(["git", "archive", "--format=tar.gz", f"--output={upload / 'bundle.tgz'}", source_sha, "deploy/oci", "deploy/keycloak"], cwd=git_repo, check=True)
            workflow_plan = dict(plan, sourceSha=source_sha, bundleSha256=hashlib.sha256((upload / "bundle.tgz").read_bytes()).hexdigest())
            workflow_plan.pop("planSha256")
            workflow_plan["planSha256"] = hashlib.sha256(json.dumps(workflow_plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            (package / "plan.json").write_text(json.dumps(workflow_plan))
            shutil.copyfile(upload / "bundle.tgz", package / "bundle.tgz")
            workflow_outer = tmp / "workflow-package.tar.gz"
            subprocess.run(["tar", "-czf", str(workflow_outer), "-C", str(package), "plan.json", "bundle.tgz"], check=True, env={**os.environ, "COPYFILE_DISABLE": "1"})
            workflow_validation = self.validate_outer(validator, workflow_outer, workflow_plan)
            self.assertEqual(workflow_validation.returncode, 0, workflow_validation.stderr)

            negative = tmp / "negative.tar.gz"
            with tarfile.open(negative, "w:gz") as archive:
                archive.add(package / "plan.json", arcname="plan.json")
                archive.add(package / "bundle.tgz", arcname="source-bundle.tgz")
            rejected = self.validate_outer(validator, negative, workflow_plan)
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn("outside the expected bundle paths", rejected.stderr)

    def test_stage_uses_candidate_installer_before_activating_host_tools(self):
        with tempfile.TemporaryDirectory(prefix="inspection-stage-installer-") as directory:
            root = pathlib.Path(directory)
            marker = root / "installer-used"
            bundle = self.make_source_bundle(root, f"#!/bin/bash\necho candidate > {marker}\n")
            plan_path, plan = self.make_plan(root, bundle)
            outer = root / "upload.tgz"
            with tarfile.open(outer, "w:gz") as archive:
                archive.add(plan_path, arcname="plan.json")
                archive.add(bundle, arcname="bundle.tgz")
            for path in (root / "run", root / "run/lock", root / "usr/local/sbin"):
                path.mkdir(parents=True, exist_ok=True)
            (root / "usr/local/sbin/inspection-install-host-tools").write_text(f"#!/bin/bash\necho stale > {marker}\n")

            bindir = root / "bin"
            bindir.mkdir()
            for name, body in {
                "id": "#!/bin/sh\necho 0\n",
                "flock": "#!/bin/sh\nexit 0\n",
                "systemd-run": "#!/bin/sh\nexit 0\n",
                "install": """#!/usr/bin/python3
import os, pathlib, shutil, sys
args=sys.argv[1:]
directory='-d' in args
while args and args[0].startswith('-'):
    flag=args.pop(0)
    if flag in ('-o','-g','-m'): args.pop(0)
if directory:
    pathlib.Path(args[-1]).mkdir(parents=True, exist_ok=True)
else:
    shutil.copyfile(args[-2], args[-1])
    os.chmod(args[-1], 0o755)
""",
            }.items():
                path = bindir / name
                path.write_text(body)
                path.chmod(0o755)

            script = (REPO / "deploy/oci/host/stage-and-apply.sh").read_text()
            for prefix in ("/run/inspection-stage-", "/run/lock", "/opt/inspection", "/var/lib/inspection-deploy", "/usr/local/sbin"):
                script = script.replace(prefix, str(root) + prefix)
            stage = root / "stage-and-apply.sh"
            stage.write_text(script)
            result = subprocess.run(
                ["bash", str(stage), plan["deploymentId"], plan["planSha256"]],
                input=outer.read_bytes(), env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}"},
                capture_output=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            self.assertEqual(marker.read_text().strip(), "candidate")

    def test_cleanup_images_accepts_receipt_generations_and_ignores_metadata(self):
        ops = (REPO / "deploy/oci/ops.sh").read_text()
        match = re.search(r"cleanup-images\).*?<<'REMOTE'\n(.*?)\nREMOTE", ops, re.S)
        self.assertIsNotNone(match, "cleanup-images remote script not found")
        catalog = json.loads((REPO / "deploy/oci/components.json").read_text())["components"]
        image_keys = sorted({item["image"] for item in catalog.values()} | {"POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE"})
        legacy_keys = ["API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE", "CAPTURE_IMAGE", "ONBOARDING_IMAGE", "POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE"]
        generations = [image_keys, [key for key in image_keys if key != "VIDEO_WORKER_IMAGE"], [key for key in image_keys if key not in {"VIDEO_WORKER_IMAGE", "LITELLM_IMAGE"}], legacy_keys]
        with tempfile.TemporaryDirectory(prefix="inspection-cleanup-images-") as directory:
            root = pathlib.Path(directory)
            receipts = root / "etc/inspection/releases"
            receipts.mkdir(parents=True)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            docker = bin_dir / "docker"
            docker.write_text("""#!/bin/bash
case "$*" in
  'image inspect '*) echo sha256:keep ;;
  'image ls '*) printf 'sha256:keep\\nsha256:unused\\n' ;;
  'ps --all '*) ;;
  'image rm '*) ;;
  *) exit 1 ;;
esac
""")
            docker.chmod(0o755)
            script = root / "cleanup.sh"
            script.write_text(match.group(1).replace("/etc/inspection/releases/", str(receipts) + "/") + "\n")
            for index, keys in enumerate(generations):
                lines = ["DEPLOYMENT_ID=deploy-cleanup-test", "PLAN_SHA256=" + "a" * 64, "SOURCE_SHA=" + "b" * 40, "COMPONENT_SOURCE_API=" + "c" * 40]
                lines.extend(f"{key}=ghcr.io/example/{key.lower()}@sha256:{'d' * 64}" for key in keys)
                (receipts / "current.env").write_text("\n".join(lines) + "\n")
                result = subprocess.run(["bash", str(script), *image_keys], env={**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"}, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, f"generation {index}: {result.stderr}")

            invalid = [key for key in image_keys if key != "VIDEO_WORKER_IMAGE"]
            invalid.remove("DASHBOARD_IMAGE")
            (receipts / "current.env").write_text("\n".join(f"{key}=ghcr.io/example/x@sha256:{'d' * 64}" for key in invalid) + "\n")
            rejected = subprocess.run(["bash", str(script), *image_keys], env={**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"}, text=True, capture_output=True)
            self.assertNotEqual(rejected.returncode, 0)

    def test_publish_and_sync_require_fifteen_image_references(self):
        publish = (REPO / ".github/workflows/publish.yml").read_text()
        sync = (REPO / ".github/workflows/sync-production-state.yml").read_text()
        self.assertIn("grep -c '_IMAGE=' active.env)\" -eq 15", publish)
        self.assertIn('[[ "$image_count" -eq 13 || "$image_count" -eq 14 || "$image_count" -eq 15 ]]', sync)


if __name__ == "__main__":
    unittest.main()
