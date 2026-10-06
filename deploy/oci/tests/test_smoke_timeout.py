import os
import pathlib
import subprocess
import tempfile
import unittest


REPO = pathlib.Path(__file__).resolve().parents[3]


class SmokeTimeoutTests(unittest.TestCase):
    def test_storage_cors_requires_one_allowed_origin_and_etag(self):
        for duplicate in (False, True):
            with self.subTest(duplicate=duplicate), tempfile.TemporaryDirectory(prefix="inspection-cors-smoke-") as directory:
                root = pathlib.Path(directory)
                (root / "etc/inspection").mkdir(parents=True)
                (root / "bin").mkdir()
                (root / "etc/inspection/public.env").write_text(
                    "ADMIN_ORIGIN=https://admin.example.test\n"
                    "DASHBOARD_ORIGIN=https://dashboard.example.test\n"
                    "CAPTURE_ORIGIN=https://capture.example.test\n"
                    "ONBOARDING_ORIGIN=https://onboarding.example.test\n"
                    "API_ORIGIN=https://api.example.test\n"
                    "OIDC_ISSUER=https://auth.example.test/realms/inspection\n"
                    "STORAGE_ORIGIN=https://storage.example.test\n"
                    "OCI_S3_BUCKET=inspection-private\n"
                )
                docker = root / "bin/docker"
                docker.write_text("""#!/bin/bash
if [[ "$*" == *' ps -q '* ]]; then echo container; exit 0; fi
if [[ "$*" == *'.State.Health'* ]]; then echo healthy; exit 0; fi
if [[ "$*" == *'.State.Status'* ]]; then echo running; exit 0; fi
exit 1
""")
                docker.chmod(0o755)
                curl = root / "bin/curl"
                curl.write_text("""#!/bin/bash
if [[ "$*" == *'--write-out'* ]]; then printf '404'; exit 0; fi
if [[ "$*" == *'--dump-header'* ]]; then
  printf 'HTTP/2 404\\r\\naccess-control-allow-origin: https://capture.example.test\\r\\n'
  if [[ "$CORS_DUPLICATE" == yes ]]; then printf 'access-control-allow-origin: *\\r\\n'; fi
  printf 'access-control-expose-headers: ETag\\r\\n\\r\\n'
fi
exit 0
""")
                curl.chmod(0o755)
                smoke = root / "smoke.sh"
                smoke.write_text((REPO / "deploy/oci/host/smoke.sh").read_text().replace("/etc/inspection", str(root / "etc/inspection")).replace("SECONDS + 300", "SECONDS + 2"))
                env = {**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}", "CORS_DUPLICATE": "yes" if duplicate else "no"}
                result = subprocess.run(["bash", str(smoke)], env=env, text=True, capture_output=True, timeout=8)
                if duplicate:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("Storage CORS smoke check timed out", result.stderr)
                else:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("private storage CORS passed", result.stdout)

    def test_public_endpoints_retry_until_the_shared_deadline(self):
        root = pathlib.Path(tempfile.mkdtemp(prefix="inspection-smoke-test-"))
        for relative in ("etc/inspection", "bin"):
            (root / relative).mkdir(parents=True, exist_ok=True)
        (root / "etc/inspection/public.env").write_text(
            "ADMIN_ORIGIN=https://admin.example.test\n"
            "DASHBOARD_ORIGIN=https://dashboard.example.test\n"
            "CAPTURE_ORIGIN=https://capture.example.test\n"
            "ONBOARDING_ORIGIN=https://onboarding.example.test\n"
            "API_ORIGIN=https://api.example.test\n"
            "OIDC_ISSUER=https://auth.example.test/realms/inspection\n"
            "STORAGE_ORIGIN=https://storage.example.test\n"
            "OCI_S3_BUCKET=inspection-private\n"
        )

        def executable(name, content):
            path = root / "bin" / name
            path.write_text(content)
            path.chmod(0o755)

        executable("docker", """#!/bin/bash
if [[ "$*" == *' ps -q '* ]]; then echo container; exit 0; fi
if [[ "$*" == *'.State.Health'* ]]; then echo healthy; exit 0; fi
if [[ "$*" == *'.State.Status'* ]]; then echo running; exit 0; fi
exit 1
""")
        executable("curl", """#!/bin/bash
printf '%s\n' "$*" >> "$CURL_TRACE"
exit 7
""")
        executable("sleep", "#!/bin/sh\n/bin/sleep .1\n")
        script = (REPO / "deploy/oci/host/smoke.sh").read_text()
        script = script.replace("/etc/inspection", str(root / "etc/inspection"))
        script = script.replace("SECONDS + 300", "SECONDS + 2")
        smoke = root / "smoke.sh"
        smoke.write_text(script)
        env = {
            **os.environ,
            "PATH": f"{root}/bin:{os.environ['PATH']}",
            "CURL_TRACE": str(root / "curl.log"),
        }
        result = subprocess.run(["bash", str(smoke)], env=env, text=True, capture_output=True, timeout=8)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Smoke check timed out: Admin", result.stderr)
        attempts = (root / "curl.log").read_text().count("https://admin.example.test/")
        self.assertGreater(attempts, 1)


if __name__ == "__main__":
    unittest.main()
