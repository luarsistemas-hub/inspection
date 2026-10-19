import importlib.util
import json
import subprocess
import unittest
from unittest.mock import patch
from pathlib import Path

MODULE = Path(__file__).parents[1] / "affected.py"
spec = importlib.util.spec_from_file_location("affected", MODULE)
affected = importlib.util.module_from_spec(spec)
spec.loader.exec_module(affected)


class AffectedTests(unittest.TestCase):
    def test_tracked_and_untracked_deploy_workflow_and_script_paths_are_classified(self):
        catalog = json.loads(affected.CATALOG.read_text())
        tracked = subprocess.check_output(["git", "ls-files"], cwd=affected.ROOT, text=True).splitlines()
        untracked = subprocess.check_output(
            ["git", "ls-files", "--others", "--exclude-standard"], cwd=affected.ROOT, text=True
        ).splitlines()
        candidates = {
            path for path in tracked + untracked
            if path.startswith(("deploy/", ".github/", "scripts/"))
        }

        def classified(path):
            if affected.matches(path, catalog["ignoredPaths"]):
                return True
            if any(affected.matches(path, rule["paths"]) for rule in catalog["sharedRules"]):
                return True
            return any(
                affected.matches(path, component["paths"])
                for component in catalog["components"].values()
            )

        unknown = sorted(path for path in candidates if not classified(path))
        self.assertEqual(unknown, [], f"Unclassified changed path(s): {unknown}")

    def test_design_system_selects_all_frontends(self):
        result = affected.resolve("", "", "all")
        self.assertEqual(len(result["targets"]), 10)

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

    def test_unused_design_system_migration_script_does_not_rebuild_frontends(self):
        catalog = json.loads(affected.CATALOG.read_text())
        for component in ("admin", "dashboard", "capture", "onboarding"):
            self.assertNotIn("scripts/lib/design-system-migration.mjs", catalog["components"][component]["paths"])

    def test_local_compose_is_ignored_and_not_a_keycloak_build_input(self):
        catalog = json.loads(affected.CATALOG.read_text())
        self.assertFalse(affected.matches("deploy/docker-compose.yml", catalog["components"]["keycloak"]["paths"]))
        self.assertTrue(affected.matches("deploy/docker-compose.yml", catalog["ignoredPaths"]))

    def test_go_dependency_walk_finds_api_command(self):
        files = affected.go_inputs("api", "inspection-api")
        self.assertIn("services/inspection/cmd/inspection-api/main.go", files)
        self.assertFalse(any(path.endswith("_test.go") for path in files))

    def test_unlisted_or_deleted_production_go_source_selects_all_go_components(self):
        paths = [
            "services/inspection/cmd/inspection-worker/dashboard_projection.go",
            "services/inspection/cmd/inspection-worker/triage_projection.go",
            "libs/identity/removed.go",
        ]
        with patch.object(affected, "git", return_value="\n".join(paths)), patch.object(
            affected, "go_inputs", return_value=set(paths)
        ):
            result = affected.resolve("base", "head")

        self.assertEqual(
            result["targets"],
            ["api", "operations", "scheduler", "video-worker", "worker"],
        )

    def test_listed_go_source_uses_its_dependency_component(self):
        path = "services/inspection/cmd/inspection-worker/main.go"

        def go_inputs(component, _command):
            return {path} if component == "worker" else set()

        with patch.object(affected, "git", return_value=path), patch.object(
            affected, "go_inputs", side_effect=go_inputs
        ):
            result = affected.resolve("base", "head")

        self.assertEqual(result["targets"], ["worker"])

    def test_go_tests_and_testdata_are_ignored(self):
        paths = [
            "services/inspection/internal/features/media/prepare_video/integration_fixture_test.go",
            "services/inspection/internal/integration/harness/config.go",
            "services/inspection/internal/integration/harness/fixtures.go",
            "services/inspection/internal/integration/harness/harness.go",
            "services/inspection/internal/integration/harness/README.md",
            "services/inspection/internal/contracts/events/testdata/registry.json",
            "libs/identity/identity_test.go",
            "libs/identity/testdata/fixture.json",
        ]
        with patch.object(affected, "git", return_value="\n".join(paths)), patch.object(
            affected, "go_inputs", return_value=set(paths)
        ):
            result = affected.resolve("base", "head")

        self.assertEqual(result["targets"], [])

    def test_inspection_build_docs_and_metadata_are_ignored(self):
        paths = [
            "services/inspection/Dockerfile",
            "services/inspection/README.md",
            "services/inspection/gqlgen.yml",
            "services/inspection/operation-manifest.json",
        ]
        with patch.object(affected, "git", return_value="\n".join(paths)), patch.object(
            affected, "go_inputs", return_value=set()
        ):
            result = affected.resolve("base", "head")

        self.assertEqual(result["targets"], [])

    def test_unknown_non_go_path_still_fails_closed(self):
        with patch.object(affected, "git", return_value="deploy/oci/novo-arquivo.bin"), patch.object(
            affected, "go_inputs", return_value=set()
        ):
            with self.assertRaisesRegex(ValueError, "Unclassified changed path"):
                affected.resolve("base", "head")


if __name__ == "__main__":
    unittest.main()
