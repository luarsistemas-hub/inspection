import json
import pathlib
import shutil
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[3]


class ComposeSecretsTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI is unavailable")
    def test_keycloak_password_recovery_is_configured_with_existing_smtp(self):
        rendered = subprocess.check_output([
            "docker", "compose", "--env-file", "deploy/oci/compose.env.example",
            "-f", "deploy/oci/compose.yaml", "--profile", "operations", "config", "--format", "json",
        ], cwd=ROOT, text=True)
        services = json.loads(rendered)["services"]
        configure = services["inspection-keycloak-configure"]
        smtp_server = json.loads(configure["environment"]["KEYCLOAK_SMTP_SERVER_JSON"])
        self.assertEqual(smtp_server["host"], "smtp.email.us-ashburn-1.oci.oraclecloud.com")
        self.assertEqual(smtp_server["starttls"], "true")
        self.assertIn("configure-inspection-realm.sh", configure["entrypoint"][0])

        realm = json.loads((ROOT / "deploy/oci/keycloak-realm.json").read_text())
        self.assertTrue(realm["resetPasswordAllowed"])
        update_password = next(action for action in realm["requiredActions"] if action["alias"] == "UPDATE_PASSWORD")
        self.assertTrue(update_password["enabled"])
        configure_script = (ROOT / "deploy/keycloak/configure-realm.sh").read_text()
        self.assertIn("authentication/register-required-action", configure_script)
        self.assertIn("authentication/required-actions/UPDATE_PASSWORD", configure_script)

    def test_local_realm_supports_password_recovery_through_mailpit(self):
        realm = json.loads((ROOT / "deploy/keycloak/inspection-realm.json").read_text())
        self.assertTrue(realm["resetPasswordAllowed"])
        self.assertEqual(realm["smtpServer"]["host"], "mailpit")
        self.assertEqual(realm["smtpServer"]["port"], "1025")
        update_password = next(action for action in realm["requiredActions"] if action["alias"] == "UPDATE_PASSWORD")
        self.assertTrue(update_password["enabled"])

        bootstrap = (ROOT / "deploy/docker-compose.yml").read_text()
        self.assertIn("configure-inspection-realm.sh", bootstrap)
        self.assertIn("smtpServer=$KEYCLOAK_SMTP_SERVER_JSON", (ROOT / "deploy/keycloak/configure-realm.sh").read_text())

    def test_password_recovery_link_has_translations(self):
        portuguese = (ROOT / "deploy/keycloak/themes/inspection/login/messages/messages_pt_BR.properties").read_text()
        english = (ROOT / "deploy/keycloak/themes/inspection/login/messages/messages_en.properties").read_text()
        self.assertIn("doForgotPassword=Esqueceu sua senha?", portuguese)
        self.assertIn("doForgotPassword=Forgot password?", english)

    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI is unavailable")
    def test_live_llm_uses_private_healthy_gateway_and_vault_key(self):
        rendered = subprocess.check_output([
            "docker", "compose", "--env-file", "deploy/oci/compose.env.example",
            "-f", "deploy/oci/compose.yaml", "--profile", "operations", "config", "--format", "json",
        ], cwd=ROOT, text=True)
        services = json.loads(rendered)["services"]
        gateway = services["litellm"]
        self.assertEqual(gateway["image"], json.loads((ROOT / "deploy/oci/base-images.lock").read_text())["images"]["litellm"])
        self.assertNotIn("ports", gateway)
        self.assertEqual(gateway["environment"]["LITELLM_MASTER_KEY"], "example")
        self.assertEqual(gateway["environment"]["LLM_API_KEY"], "example")
        self.assertEqual(services["inspection-worker"]["depends_on"]["litellm"]["condition"], "service_healthy")
        for name, service in services.items():
            environment = service.get("environment", {})
            if "INSPECTION_LLM_MODE" in environment:
                with self.subTest(service=name):
                    self.assertEqual(environment["INSPECTION_LLM_MODE"], "live")
                    self.assertEqual(environment["INSPECTION_LITELLM_URL"], "http://litellm:4000")
                    self.assertEqual(environment["INSPECTION_LITELLM_API_KEY"], gateway["environment"]["LITELLM_MASTER_KEY"])

    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI is unavailable")
    def test_worker_uses_worker_database_connection(self):
        rendered = subprocess.check_output([
            "docker", "compose", "--env-file", "deploy/oci/compose.env.example",
            "-f", "deploy/oci/compose.yaml", "config", "--format", "json",
        ], cwd=ROOT, text=True)
        services = json.loads(rendered)["services"]
        worker = services["inspection-worker"]["environment"]
        runtime = services["inspection-api"]["environment"]
        self.assertEqual(worker["INSPECTION_DATABASE_URL"], worker["INSPECTION_DISPATCHER_DATABASE_URL"])
        self.assertNotEqual(worker["INSPECTION_DATABASE_URL"], runtime["INSPECTION_DATABASE_URL"])

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
