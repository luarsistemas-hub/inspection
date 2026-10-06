import json
import os
import pathlib
import subprocess
import tempfile
import unittest


SOURCE = pathlib.Path(__file__).resolve().parents[1] / "host/deployment-status.sh"


class DeploymentStatusTests(unittest.TestCase):
    def status(self, **properties):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for name, body in {
                "id": "echo 0",
                "systemctl": 'printf "%s\\n" "$PROPERTIES"',
            }.items():
                path = root / name
                path.write_text("#!/bin/sh\n" + body + "\n")
                path.chmod(0o755)
            result = subprocess.run(
                ["bash", str(SOURCE), "deploy-test-1"],
                env={**os.environ, "PATH": f"{root}:{os.environ['PATH']}",
                     "PROPERTIES": "\n".join(f"{k}={v}" for k, v in properties.items())},
                capture_output=True, text=True, check=True,
            )
            return json.loads(result.stdout)["status"]

    def test_oneshot_starting_with_default_success_is_running(self):
        self.assertEqual(self.status(LoadState="loaded", ActiveState="activating",
                                     SubState="start", Result="success", ExecMainStatus="0"), "running")

    def test_missing_unit_is_unknown_despite_default_success(self):
        self.assertEqual(self.status(LoadState="not-found", ActiveState="inactive",
                                     SubState="dead", Result="success", ExecMainStatus="0"), "unknown")

    def test_success_requires_completed_retained_oneshot(self):
        properties = dict(LoadState="loaded", ActiveState="active", SubState="exited",
                          Result="success", ExecMainStatus="0")
        self.assertEqual(self.status(**properties), "unknown")
        self.assertEqual(self.status(**properties, ExecMainExitTimestamp="Tue 2026-10-06 21:27:06 UTC"), "succeeded")

    def test_failed_and_running_states(self):
        for active, sub, result, exit_code, expected in (
            ("failed", "failed", "exit-code", "1", "failed"),
            ("failed", "failed", "timeout", "0", "failed"),
            ("active", "running", "success", "0", "running"),
            ("deactivating", "stop", "success", "0", "running"),
            ("inactive", "dead", "success", "0", "unknown"),
        ):
            with self.subTest(active=active, result=result):
                self.assertEqual(self.status(LoadState="loaded", ActiveState=active, SubState=sub,
                                             Result=result, ExecMainStatus=exit_code), expected)


if __name__ == "__main__":
    unittest.main()
