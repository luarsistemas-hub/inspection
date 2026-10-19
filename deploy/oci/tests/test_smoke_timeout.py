import os
import pathlib
import subprocess
import tempfile
import unittest


REPO = pathlib.Path(__file__).resolve().parents[3]


class SmokeTimeoutTests(unittest.TestCase):
    def test_storage_cors_requires_one_allowed_origin_and_etag(self):
        scenarios = ((False, False, ""), (True, False, ""), (False, True, ""),
                     (False, False, "ffmpeg"), (False, False, "ffprobe"), (False, False, "scratch"),
                     (False, False, "metrics"), (False, False, "empty-metrics"), (False, False, "no-token"))
        for duplicate, missing_video, failure in scenarios:
            with self.subTest(duplicate=duplicate, missing_video=missing_video, failure=failure), tempfile.TemporaryDirectory(prefix="inspection-cors-smoke-") as directory:
                root = pathlib.Path(directory)
                (root / "etc/inspection").mkdir(parents=True)
                (root / "components.json").write_text('{"components":{"old":{"image":"OLD_IMAGE"}}}')
                tools = root / "host-tools/abc123"
                tools.mkdir(parents=True)
                (tools / "components.json").write_bytes((REPO / "deploy/oci/components.json").read_bytes())
                (tools / "component-catalog.sh").write_bytes((REPO / "deploy/oci/host/component-catalog.sh").read_bytes())
                (root / "compose.yaml").write_text("services: {}\n")
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
                (root / "etc/inspection/secrets.d").mkdir()
                if failure != "no-token":
                    (root / "etc/inspection/secrets.d/INSPECTION_METRICS_TOKEN").write_text("metrics-fixture\n")
                docker = root / "bin/docker"
                docker.write_text("""#!/bin/bash
if [[ "$*" == *' config --services' ]]; then
  if [[ "$SMOKE_OMIT_VIDEO" == yes ]]; then printf 'inspection-api\\ninspection-worker\\ninspection-scheduler\\nadmin\\ndashboard\\ncapture\\nonboarding\\nkeycloak\\n'; else printf 'inspection-api\\ninspection-worker\\ninspection-scheduler\\ninspection-video-worker\\nadmin\\ndashboard\\ncapture\\nonboarding\\nkeycloak\\n'; fi
  exit 0
fi
if [[ "$*" == *' config --environment' ]]; then printf 'VIDEO_WORKER_IMAGE=example\\n'; exit 0; fi
if [[ "$*" == *' ps -q '* ]]; then echo container; exit 0; fi
if [[ "$*" == *' exec -T inspection-video-worker '* ]]; then
  [[ "$SMOKE_FAIL" != ffmpeg || "$*" != *' ffmpeg -version'* ]] || exit 1
  [[ "$SMOKE_FAIL" != ffprobe || "$*" != *' ffprobe -version'* ]] || exit 1
  [[ "$SMOKE_FAIL" != scratch || "$*" != *'var/lib/inspection-video-scratch'* ]] || exit 1
  exit 0
fi
if [[ "$*" == *'.State.Health'* ]]; then echo healthy; exit 0; fi
if [[ "$*" == *'.State.Status'* ]]; then echo running; exit 0; fi
exit 1
""")
                docker.chmod(0o755)
                curl = root / "bin/curl"
                curl.write_text("""#!/bin/bash
if [[ "$*" == *'--write-out'* ]]; then printf '404'; exit 0; fi
if [[ "$*" == *'/metrics'* ]]; then
  [[ "$SMOKE_FAIL" != metrics ]] || exit 22
  [[ "$SMOKE_FAIL" == empty-metrics ]] || echo 'inspection_video_queue_depth 0'
  exit 0
fi
if [[ "$*" == *'--dump-header'* ]]; then
  printf 'HTTP/2 404\\r\\naccess-control-allow-origin: https://capture.example.test\\r\\n'
  if [[ "$CORS_DUPLICATE" == yes ]]; then printf 'access-control-allow-origin: *\\r\\n'; fi
  printf 'access-control-expose-headers: ETag\\r\\n\\r\\n'
fi
exit 0
""")
                curl.chmod(0o755)
                smoke = tools / "smoke.sh"
                smoke.write_text((REPO / "deploy/oci/host/smoke.sh").read_text().replace("/etc/inspection", str(root / "etc/inspection")).replace("SECONDS + 300", "SECONDS + 2"))
                env = {**os.environ, "PATH": f"{root}/bin:{os.environ['PATH']}", "CORS_DUPLICATE": "yes" if duplicate else "no", "SMOKE_OMIT_VIDEO": "yes" if missing_video else "no", "SMOKE_FAIL": failure, "INSPECTION_COMPOSE_FILE": str(root / "compose.yaml")}
                result = subprocess.run(["bash", str(smoke)], env=env, text=True, capture_output=True, timeout=8)
                if failure:
                    self.assertNotEqual(result.returncode, 0)
                elif missing_video:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("Compose image VIDEO_WORKER_IMAGE is configured but service", result.stderr)
                elif duplicate:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("Storage CORS smoke check timed out", result.stderr)
                else:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("private storage CORS passed", result.stdout)

    def test_public_endpoints_retry_until_the_shared_deadline(self):
        root = pathlib.Path(tempfile.mkdtemp(prefix="inspection-smoke-test-"))
        for relative in ("etc/inspection", "bin"):
            (root / relative).mkdir(parents=True, exist_ok=True)
        (root / "etc/inspection/secrets.d").mkdir()
        (root / "etc/inspection/secrets.d/INSPECTION_METRICS_TOKEN").write_text("metrics-fixture\n")
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
        (root / "components.json").write_text('{"components":{"old":{"image":"OLD_IMAGE"}}}')
        tools = root / "host-tools/abc123"
        tools.mkdir(parents=True)
        (tools / "components.json").write_bytes((REPO / "deploy/oci/components.json").read_bytes())
        (tools / "component-catalog.sh").write_bytes((REPO / "deploy/oci/host/component-catalog.sh").read_bytes())
        (root / "compose.yaml").write_text("services: {}\n")

        def executable(name, content):
            path = root / "bin" / name
            path.write_text(content)
            path.chmod(0o755)

        executable("docker", """#!/bin/bash
if [[ "$*" == *' config --services' ]]; then printf 'inspection-api\\ninspection-worker\\ninspection-scheduler\\nadmin\\ndashboard\\ncapture\\nonboarding\\nkeycloak\\n'; exit 0; fi
if [[ "$*" == *' config --environment' ]]; then exit 0; fi
if [[ "$*" == *' ps -q '* ]]; then echo container; exit 0; fi
if [[ "$*" == *' exec -T inspection-video-worker '* ]]; then exit 0; fi
if [[ "$*" == *'.State.Health'* ]]; then echo healthy; exit 0; fi
if [[ "$*" == *'.State.Status'* ]]; then echo running; exit 0; fi
exit 1
""")
        executable("curl", """#!/bin/bash
printf '%s\n' "$*" >> "$CURL_TRACE"
if [[ "$*" == *'/metrics'* ]]; then echo 'inspection_video_queue_depth 0'; exit 0; fi
exit 7
""")
        executable("sleep", "#!/bin/sh\n/bin/sleep .1\n")
        script = (REPO / "deploy/oci/host/smoke.sh").read_text()
        script = script.replace("/etc/inspection", str(root / "etc/inspection"))
        script = script.replace("SECONDS + 300", "SECONDS + 2")
        smoke = tools / "smoke.sh"
        smoke.write_text(script)
        env = {
            **os.environ,
            "PATH": f"{root}/bin:{os.environ['PATH']}",
            "CURL_TRACE": str(root / "curl.log"),
            "INSPECTION_COMPOSE_FILE": str(root / "compose.yaml"),
        }
        result = subprocess.run(["bash", str(smoke)], env=env, text=True, capture_output=True, timeout=8)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Smoke check timed out: Admin", result.stderr)
        attempts = (root / "curl.log").read_text().count("https://admin.example.test/")
        self.assertGreater(attempts, 1)


if __name__ == "__main__":
    unittest.main()
