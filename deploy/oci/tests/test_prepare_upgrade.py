import os
import pathlib
import subprocess
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "host/prepare-upgrade.sh"


class PrepareUpgradeTests(unittest.TestCase):
    def scenario(self, *, mount=True, uuid="expected-uuid", writable=True, free_bytes=9 * 1024**3):
        temp = tempfile.TemporaryDirectory(prefix="inspection-prepare-upgrade-")
        self.addCleanup(temp.cleanup)
        root = pathlib.Path(temp.name)
        mount_path = root / "srv/inspection"
        mount_path.mkdir(parents=True)
        if not mount:
            (root / "unmounted").mkdir()
        (root / "etc/inspection").mkdir(parents=True)
        (root / "etc/inspection/data-volume.uuid").write_text("expected-uuid\n")
        sentinel = mount_path / "video-scratch/accepted-work"
        sentinel.parent.mkdir()
        sentinel.write_text("preserve")
        bindir = root / "bin"
        bindir.mkdir()

        def executable(name, body):
            path = bindir / name
            path.write_text(body)
            path.chmod(0o755)

        executable("id", "#!/bin/sh\necho 0\n")
        executable("mountpoint", f"#!/bin/sh\n[ \"$2\" = \"{mount_path}\" ] && {'true' if mount else 'false'}\n")
        executable("findmnt", f"#!/bin/sh\necho {uuid}\n")
        executable("install", "#!/bin/sh\nwhile [ $# -gt 1 ]; do shift; done\nmkdir -p \"$1\"\n")
        executable("chown", "#!/bin/sh\nexit 0\n")
        executable("chmod", "#!/bin/sh\nexit 0\n")
        executable("python3", f"#!/bin/sh\necho {free_bytes}\n")
        executable("docker", f"""#!/bin/sh
case "$*" in
  *'id -u'*) echo '1001:1002' ;;
  *'.upgrade-check-'*) {'exit 0' if writable else 'exit 1'} ;;
  *) exit 9 ;;
esac
""")
        result = subprocess.run(
            ["bash", str(SCRIPT), "example.test/video@sha256:" + "a" * 64],
            env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}", "INSPECTION_HOST_ROOT": str(root)},
            capture_output=True, text=True,
        )
        return result, sentinel

    def test_creates_scratch_and_preserves_existing_files(self):
        result, sentinel = self.scenario()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sentinel.read_text(), "preserve")

    def test_wrong_uuid_missing_mount_unwritable_and_low_space_fail(self):
        scenarios = (({"uuid": "wrong"}, "UUID"), ({"mount": False}, "not mounted"),
                     ({"writable": False}, "returned non-zero"), ({"free_bytes": 7 * 1024**3}, "8 GiB"))
        for args, expected in scenarios:
            with self.subTest(args=args):
                result, _ = self.scenario(**args)
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(expected in result.stderr or (expected == "returned non-zero" and result.returncode != 0), result.stderr)


if __name__ == "__main__":
    unittest.main()
