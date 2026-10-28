import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


REPO = pathlib.Path(__file__).resolve().parents[3]
HOST = REPO / "deploy/oci/host"
OCI = REPO / "deploy/oci"
VERSION = "a" * 40


class HostCatalogTests(unittest.TestCase):
    def test_release_tree_smoke_resolves_parent_catalog(self):
        with tempfile.TemporaryDirectory(prefix="inspection-smoke-catalog-") as directory:
            root = pathlib.Path(directory)
            release_oci = root / "opt/inspection/releases" / VERSION / "deploy/oci"
            host = release_oci / "host"
            host.mkdir(parents=True)
            shutil.copy2(OCI / "components.json", release_oci / "components.json")
            shutil.copy2(HOST / "component-catalog.sh", host / "component-catalog.sh")
            smoke = HOST / "smoke.sh"
            script = smoke.read_text().replace("/etc/inspection", str(root / "etc/inspection"))
            target = host / "smoke.sh"
            target.write_text(script)
            target.chmod(0o755)
            public_env = root / "etc/inspection/public.env"
            public_env.parent.mkdir(parents=True)
            public_env.write_text("\n".join(f"{key}=https://example.test" for key in (
                "ADMIN_ORIGIN", "DASHBOARD_ORIGIN", "CAPTURE_ORIGIN", "ONBOARDING_ORIGIN",
                "API_ORIGIN", "OIDC_ISSUER", "STORAGE_ORIGIN")) + "\nOCI_S3_BUCKET=inspection\n")
            secrets = root / "etc/inspection/secrets.d"
            secrets.mkdir(parents=True)
            (secrets / "INSPECTION_METRICS_TOKEN").write_text("metrics-fixture\n")
            (root / "etc/inspection/compose.env").write_text("API_IMAGE=example.test/api@sha256:" + "b" * 64 + "\n")
            bindir = root / "bin"
            bindir.mkdir()
            docker = bindir / "docker"
            docker.write_text("""#!/bin/bash
case "$*" in
  *'config --services'*) printf '%s\\n' inspection-api inspection-worker inspection-scheduler inspection-video-worker inspection-operations admin dashboard capture onboarding keycloak caddy litellm; exit 0 ;;
  *'config --environment'*) echo API_IMAGE=example.test/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb; exit 0 ;;
  *'ps -q'*) echo container-id; exit 0 ;;
esac
if [[ "$*" == *"{{if .State.Health}}"* ]]; then echo healthy; elif [[ "$*" == *"{{.State.Status}}"* ]]; then echo running; fi
""")
            docker.chmod(0o755)
            curl = bindir / "curl"
            curl.write_text("""#!/bin/bash
if [[ "$*" == *'%{http_code}'* ]]; then echo 403; exit 0; fi
if [[ "$*" == *'/metrics'* ]]; then echo 'inspection_video_queue_depth 0'; exit 0; fi
if [[ "$*" == *'--dump-header'* ]]; then
  printf 'HTTP/1.1 404 Not Found\\r\\nAccess-Control-Allow-Origin: https://example.test\\r\\nAccess-Control-Expose-Headers: ETag\\r\\n\\r\\n'
fi
exit 0
""")
            curl.chmod(0o755)
            result = subprocess.run(["bash", str(target)], env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}",
                "INSPECTION_COMPOSE_ENV": str(root / "etc/inspection/compose.env"),
                "INSPECTION_RELEASES_ROOT": str(root / "opt/inspection/releases")}, text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Compose health, public HTTPS", result.stdout)

    def test_run_deployment_atomically_updates_static_installer(self):
        with tempfile.TemporaryDirectory(prefix="inspection-run-deploy-") as directory:
            root = pathlib.Path(directory)
            release = root / "opt/inspection/releases" / VERSION
            host = release / "deploy/oci/host"
            host.mkdir(parents=True)
            shutil.copy2(HOST / "install-host-tools.sh", host / "install-host-tools.sh")
            apply = host / "apply-plan.sh"
            apply.write_text("#!/bin/sh\necho applied > \"$APPLIED_MARKER\"\n")
            apply.chmod(0o755)
            job = root / "var/lib/inspection-deploy/jobs/deploy-test-1"
            job.mkdir(parents=True)
            plan = {"schemaVersion": 2, "operation": "deploy"}
            plan["planSha256"] = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            plan_path = job / "plan.json"
            plan_path.write_text(json.dumps(plan))
            static_dir = root / "usr/local/sbin"
            static_dir.mkdir(parents=True)
            static = static_dir / "inspection-install-host-tools"
            static.write_text("old static installer\n")
            (root / "run/lock").mkdir(parents=True)
            bindir = root / "bin"
            bindir.mkdir()
            (bindir / "id").write_text("#!/bin/sh\necho 0\n")
            (bindir / "flock").write_text("#!/bin/sh\nexit 0\n")
            install = bindir / "install"
            install.write_text("""#!/usr/bin/python3
import os, shutil, sys
args=sys.argv[1:]
while args and args[0].startswith('-'):
    flag=args.pop(0)
    if flag in ('-o','-g','-m'): args.pop(0)
shutil.copyfile(args[0], args[1])
os.chmod(args[1], 0o755)
""")
            for name in ("id", "flock", "install"):
                (bindir / name).chmod(0o755)
            script = (HOST / "run-deployment.sh").read_text()
            for prefix in ("/opt/inspection", "/var/lib/inspection-deploy", "/run/lock", "/usr/local/sbin"):
                script = script.replace(prefix, str(root) + prefix)
            runner = root / "run-deployment.sh"
            runner.write_text(script)
            result = subprocess.run(["bash", str(runner), "deploy-test-1", VERSION, plan["planSha256"]], env={
                **os.environ, "PATH": f"{bindir}:{os.environ['PATH']}", "APPLIED_MARKER": str(root / "applied")}, text=True, capture_output=True)
            updated_installer = static.read_text() if static.exists() else ""
            temporary_remains = list(static_dir.glob("inspection-install-host-tools.new.*"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(updated_installer, (HOST / "install-host-tools.sh").read_text())
        self.assertFalse(temporary_remains)

    def installer_fixture(self, root, version=VERSION):
        source = root / "bundle/deploy/oci/host"
        source.mkdir(parents=True)
        for name in ("deploy-release.sh", "apply-plan.sh", "stage-and-apply.sh", "run-deployment.sh", "export-state.sh",
                     "deployment-status.sh", "secrets-refresh.sh", "smoke.sh", "prepare-upgrade.sh", "publish-metrics.py", "ssh-command.sh", "component-catalog.sh"):
            shutil.copy2(HOST / (name if name != "component-catalog.sh" else name), source / name)
        (source.parent / "components.json").write_bytes((OCI / "components.json").read_bytes())
        target = root / "opt/inspection/host-tools" / version
        target.mkdir(parents=True)
        for name in ("deploy-release.sh", "apply-plan.sh", "stage-and-apply.sh", "run-deployment.sh", "export-state.sh",
                     "deployment-status.sh", "secrets-refresh.sh", "smoke.sh", "ssh-command.sh"):
            shutil.copy2(source / name, target / name)
        return source, target

    def invoke_installer(self, root, source):
        bindir = root / "bin"
        bindir.mkdir(exist_ok=True)
        for name, body in (("id", "#!/bin/sh\\necho 0\\n"), ("chown", "#!/bin/sh\\nexit 0\\n")):
            path = bindir / name
            path.write_text(body.replace("\\n", "\n"))
            path.chmod(0o755)
        install = bindir / "install"
        install.write_text("""#!/usr/bin/python3
import os, pathlib, shutil, sys
args=sys.argv[1:]
directory='-d' in args
mode=None
while args and args[0].startswith('-'):
    flag=args.pop(0)
    if flag in ('-o','-g','-m'):
        value=args.pop(0)
        if flag == '-m': mode=int(value, 8)
if directory:
    path=pathlib.Path(args[-1])
    path.mkdir(parents=True, exist_ok=True)
    if mode is not None: os.chmod(path, mode)
else:
    shutil.copyfile(args[-2], args[-1])
    os.chmod(args[-1], int('755' if args[-2].endswith('.sh') else '644', 8))
""")
        install.chmod(0o755)
        chmod = bindir / "chmod"
        chmod.write_text("#!/bin/sh\nexec /bin/chmod \"$@\"\n")
        chmod.chmod(0o755)
        mv = bindir / "mv"
        mv.write_text("#!/bin/sh\nif [ \"$1\" = -Tf ]; then shift; fi\nexec /bin/mv -f \"$@\"\n")
        mv.chmod(0o755)
        script = (HOST / "install-host-tools.sh").read_text().replace("/opt/inspection", str(root / "opt/inspection")).replace("/usr/local/sbin", str(root / "usr/local/sbin"))
        (root / "usr/local/sbin").mkdir(parents=True, exist_ok=True)
        installer = root / "installer.sh"
        installer.write_text(script)
        return subprocess.run(["bash", str(installer), VERSION, str(source)], env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}"}, text=True, capture_output=True)

    def test_installer_completes_legacy_version_without_catalog(self):
        with tempfile.TemporaryDirectory(prefix="inspection-install-legacy-") as directory:
            root = pathlib.Path(directory)
            source, target = self.installer_fixture(root)
            result = self.invoke_installer(root, source)
            has_catalog = (target / "components.json").is_file()
            has_helper = (target / "component-catalog.sh").is_file()
            has_prepare_upgrade = (target / "prepare-upgrade.sh").is_file()
            ssh_launcher = root / "usr/local/sbin/inspection-ssh-command"
            has_ssh_launcher = ssh_launcher.is_symlink() and "export-state[[:space:]]+(current|previous|blocked)" in ssh_launcher.read_text()
            has_publisher = (target / "publish-metrics.py").is_file()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(has_catalog)
        self.assertTrue(has_helper)
        self.assertTrue(has_prepare_upgrade)
        self.assertTrue(has_ssh_launcher)
        self.assertTrue(has_publisher)

    def test_installer_keeps_restricted_ssh_path_traversable(self):
        with tempfile.TemporaryDirectory(prefix="inspection-install-permissions-") as directory:
            root = pathlib.Path(directory)
            source, target = self.installer_fixture(root)
            host_tools = target.parent
            host_tools.chmod(0o750)
            target.chmod(0o750)
            result = self.invoke_installer(root, source)
            root_mode = host_tools.stat().st_mode & 0o777
            target_mode = target.stat().st_mode & 0o777
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(root_mode, 0o755)
        self.assertEqual(target_mode, 0o755)

    def test_installer_rejects_real_script_divergence(self):
        with tempfile.TemporaryDirectory(prefix="inspection-install-diverge-") as directory:
            root = pathlib.Path(directory)
            source, target = self.installer_fixture(root)
            (target / "apply-plan.sh").write_text("different\n")
            result = self.invoke_installer(root, source)
            error = result.stderr
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("different content", error)

    def test_installer_rejects_existing_catalog_divergence(self):
        with tempfile.TemporaryDirectory(prefix="inspection-install-catalog-diverge-") as directory:
            root = pathlib.Path(directory)
            source, target = self.installer_fixture(root)
            (target / "components.json").write_text('{"components":{}}\n')
            result = self.invoke_installer(root, source)
            error = result.stderr
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("different content", error)


if __name__ == "__main__":
    unittest.main()
