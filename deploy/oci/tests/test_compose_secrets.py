import json
import pathlib
import shutil
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[3]


class ComposeSecretsTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI is unavailable")
    def test_privileged_database_url_is_only_in_migration_operations(self):
        rendered = subprocess.check_output([
            "docker", "compose", "--env-file", "deploy/oci/compose.env.example",
            "-f", "deploy/oci/compose.yaml", "--profile", "operations", "config", "--format", "json",
        ], cwd=ROOT, text=True)
        services = json.loads(rendered)["services"]
        privileged = {
            name for name, service in services.items()
            if "INSPECTION_MIGRATION_DATABASE_URL" in service.get("environment", {})
        }
        self.assertEqual(privileged, {"inspection-migrate", "inspection-prompt-seed"})
        self.assertNotIn("INSPECTION_MIGRATION_DATABASE_URL", services["inspection-api"]["environment"])
        self.assertNotIn("INSPECTION_MIGRATION_DATABASE_URL", services["inspection-worker"]["environment"])
        self.assertNotIn("INSPECTION_MIGRATION_DATABASE_URL", services["inspection-scheduler"]["environment"])

    @unittest.skipUnless(shutil.which("jq"), "jq is unavailable")
    def test_new_realm_import_applies_configured_super_admin_subject(self):
        script = (ROOT / "deploy/oci/host/secrets-refresh.sh").read_text()
        self.assertIn(".users[0].id=$subject", script)
        imported = subprocess.check_output([
            "jq", "--arg", "subject", "qa-user-id", ".users[0].id=$subject",
            "deploy/oci/keycloak-realm.json",
        ], cwd=ROOT, text=True)
        user = json.loads(imported)["users"][0]
        self.assertEqual(user["username"], "inspection-super-admin")
        self.assertEqual(user["id"], "qa-user-id")


if __name__ == "__main__":
    unittest.main()
