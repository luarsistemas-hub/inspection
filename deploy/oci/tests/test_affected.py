import importlib.util
import json
import unittest
from pathlib import Path

MODULE = Path(__file__).parents[1] / "affected.py"
spec = importlib.util.spec_from_file_location("affected", MODULE)
affected = importlib.util.module_from_spec(spec)
spec.loader.exec_module(affected)


class AffectedTests(unittest.TestCase):
    def test_design_system_selects_all_frontends(self):
        result = affected.resolve("", "", "all")
        self.assertEqual(len(result["targets"]), 9)

    def test_unknown_component_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "Unknown component"):
            affected.resolve("", "", "selected", ["legacy"])

    def test_match_directory_prefix(self):
        self.assertTrue(affected.matches("apps/admin/src/page.tsx", ["apps/admin/"]))
        self.assertFalse(affected.matches("apps/administrator/a", ["apps/admin/"]))

    def test_address_package_is_an_input_to_admin_and_onboarding_only(self):
        catalog = json.loads(affected.CATALOG.read_text())
        for component in ("admin", "onboarding"):
            self.assertIn("packages/inspection-address/", catalog["components"][component]["paths"])
        for component in ("dashboard", "capture"):
            self.assertNotIn("packages/inspection-address/", catalog["components"][component]["paths"])

    def test_local_compose_is_ignored_and_not_a_keycloak_build_input(self):
        catalog = json.loads(affected.CATALOG.read_text())
        self.assertFalse(affected.matches("deploy/docker-compose.yml", catalog["components"]["keycloak"]["paths"]))
        self.assertTrue(affected.matches("deploy/docker-compose.yml", catalog["ignoredPaths"]))

    def test_go_dependency_walk_finds_api_command(self):
        files = affected.go_inputs("api", "inspection-api")
        self.assertIn("services/inspection/cmd/inspection-api/main.go", files)


if __name__ == "__main__":
    unittest.main()
