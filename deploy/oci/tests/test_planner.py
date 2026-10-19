import importlib.util
import unittest
import tempfile
import subprocess
import pathlib
from unittest.mock import patch
from pathlib import Path

MODULE = Path(__file__).parents[1] / "planner.py"
spec = importlib.util.spec_from_file_location("planner", MODULE)
planner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(planner)


def component(name):
    record = {"image": f"ghcr.io/example/inspection-{name}@sha256:" + "a" * 64,
              "sourceSha": "b" * 40, "fingerprint": "c" * 64}
    profiles = {
        "admin": {"API_ORIGIN":"https://api.example.com","AUTH_ORIGIN":"https://auth.example.com","DASHBOARD_ORIGIN":"https://dashboard.example.com","CAPTURE_ORIGIN":"https://capture.example.com"},
        "dashboard": {"API_ORIGIN":"https://api.example.com","AUTH_ORIGIN":"https://auth.example.com","ADMIN_ORIGIN":"https://admin.example.com","STORAGE_ORIGIN":"https://storage.example.com"},
        "capture": {"API_ORIGIN":"https://api.example.com","STORAGE_ORIGIN":"https://storage.example.com"},
        "onboarding": {"API_ORIGIN":"https://api.example.com","TURNSTILE_SITE_KEY":"sitekey"},
    }
    if name in profiles:
        record["publicProfile"] = profiles[name]
    return record


class PlannerTests(unittest.TestCase):
    def setUp(self):
        self.candidate = {"candidateId": "build-17", "sourceSha": "b" * 40,
                          "components": {name: component(name) for name in planner.COMPONENTS},
                          "infrastructure": {key: f"docker.io/example/{key.lower()}@sha256:" + "e" * 64
                                             for key in planner.INFRA_KEYS},
                          "compatibility": {"databaseChange": "none", "contractChange": "compatible"}}
        self.active = {key: f"ghcr.io/example/inspection-{name}@sha256:" + "d" * 64
                       for name, key in planner.IMAGE_KEYS.items()}
        self.active.update({key: f"docker.io/example/{key.lower()}@sha256:" + "e" * 64
                            for key in planner.INFRA_KEYS})
        self.sources = {name: "a" * 40 for name in planner.COMPONENTS}

    def test_selective_update_preserves_other_active_images(self):
        self.candidate["components"]["admin"]["sourceSha"] = "a" * 40
        with patch.object(planner, "infer_compatibility", return_value={"databaseChange":"none", "contractChange":"compatible"}):
            plan = planner.create_plan(self.active, self.candidate, ["admin"], "deploy-17", active_sources=self.sources)
        self.assertEqual(plan["desiredImages"]["ADMIN_IMAGE"], self.candidate["components"]["admin"]["image"])
        self.assertEqual(plan["desiredImages"]["WORKER_IMAGE"], self.active["WORKER_IMAGE"])
        self.assertFalse(plan["operations"]["runMigration"])
        self.assertFalse(plan["operations"]["runSeed"])
        self.assertEqual(plan["componentSources"]["admin"], "a" * 40)

    def test_database_change_requires_coordinated_processes(self):
        with patch.object(planner, "infer_compatibility", return_value={"databaseChange":"required", "contractChange":"compatible"}):
            plan = planner.create_plan(self.active, self.candidate, ["api"], active_sources=self.sources)
        self.assertTrue(plan["operations"]["runMigration"])
        self.assertTrue(plan["operations"]["runSeed"])
        self.assertEqual(set(plan["targets"]), planner.GO_COMPONENTS)

    def test_first_deploy_requires_full_composition(self):
        with self.assertRaisesRegex(ValueError, "first deployment"):
            planner.create_plan({}, self.candidate, ["admin"])

    def test_missing_candidate_target_is_not_added_implicitly(self):
        del self.candidate["components"]["admin"]
        with self.assertRaisesRegex(ValueError, "composition is incomplete"):
            planner.create_plan(self.active, self.candidate, ["admin"])

    def test_unknown_contract_impact_requires_full_selection(self):
        with patch.object(planner, "infer_compatibility", return_value={"databaseChange":"none", "contractChange":"unknown"}):
            with self.assertRaisesRegex(ValueError, "unverified contract change"):
                planner.create_plan(self.active, self.candidate, ["api"], active_sources=self.sources)

    def test_candidate_compatibility_metadata_does_not_override_source_comparison(self):
        self.candidate["compatibility"] = []
        with self.assertRaisesRegex(ValueError, "coordinated selection"):
            planner.create_plan(self.active, self.candidate, ["admin"], active_sources=self.sources)

    def test_rollback_changes_only_requested_component_and_never_migrates(self):
        previous = {key: value.replace("d" * 64, "f" * 64) for key, value in self.active.items()}
        config_hash = "9" * 64
        plan = planner.create_rollback_plan(self.active, previous, ["worker"], "rollback-17", "a" * 40,
            runtime_config_sha=config_hash, active_runtime_config_sha=config_hash)
        self.assertEqual(plan["desiredImages"]["WORKER_IMAGE"], previous["WORKER_IMAGE"])
        self.assertEqual(plan["desiredImages"]["API_IMAGE"], previous["API_IMAGE"])
        self.assertEqual(set(plan["targets"]), planner.GO_COMPONENTS)
        self.assertFalse(plan["operations"]["runMigration"])

    def test_rollback_preserves_component_origins_and_omits_incomplete_origins(self):
        previous = {key: value.replace("d" * 64, "f" * 64) for key, value in self.active.items()}
        old_sources = {name: "a" * 40 for name in planner.COMPONENTS}
        active_sources = {name: "b" * 40 for name in planner.COMPONENTS}
        config_hash = "9" * 64
        plan = planner.create_rollback_plan(self.active, previous, ["worker"], source_sha="a"*40,
            active_sources=active_sources, previous_sources=old_sources, runtime_config_sha=config_hash,
            active_runtime_config_sha=config_hash)
        self.assertEqual(plan["componentSources"]["worker"], "a" * 40)
        self.assertEqual(plan["componentSources"]["admin"], "b" * 40)
        # Missing any origin omits the whole map, keeping later compatibility conservative.
        incomplete = dict(old_sources)
        incomplete.pop("admin")
        plan = planner.create_rollback_plan(self.active, previous, ["worker"], source_sha="a"*40,
            active_sources=active_sources, previous_sources=incomplete, runtime_config_sha=config_hash,
            active_runtime_config_sha=config_hash)
        self.assertNotIn("componentSources", plan)

    def test_rollback_requires_matching_config_hash_and_video_target(self):
        previous = dict(self.active)
        with self.assertRaisesRegex(ValueError, "matching runtime configuration"):
            planner.create_rollback_plan(self.active, previous, ["admin"], source_sha="a"*40,
                runtime_config_sha="1"*64, active_runtime_config_sha="2"*64)
        no_video = {key:value for key,value in previous.items() if key != "VIDEO_WORKER_IMAGE"}
        with self.assertRaisesRegex(ValueError, "no video-worker"):
            planner.create_rollback_plan(self.active, no_video, ["worker"], source_sha="a"*40,
                runtime_config_sha="1"*64, active_runtime_config_sha="1"*64)

    def test_runtime_config_hash_is_stable_and_ignores_themes(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            (root/"deploy/oci").mkdir(parents=True)
            (root/"deploy/keycloak/themes/example").mkdir(parents=True)
            (root/"deploy/oci/compose.yaml").write_text("services: {}\n")
            (root/"deploy/oci/Caddyfile").write_text(":80 {}\n")
            (root/"deploy/oci/litellm-config.yaml").write_text("model_list: []\n")
            (root/"deploy/oci/keycloak-realm.json").write_text("{}\n")
            (root/"deploy/keycloak/realm.json").write_text("{}\n")
            first=planner.runtime_config_sha256(root)
            (root/"deploy/keycloak/themes/example/theme.css").write_text("changed")
            self.assertEqual(first, planner.runtime_config_sha256(root))

    def test_first_gateway_deployment_requires_worker_and_tracks_image(self):
        old_active = {key: value for key, value in self.active.items() if key != "LITELLM_IMAGE"}
        with self.assertRaisesRegex(ValueError, "complete ten-component"):
            planner.create_plan(old_active, self.candidate, ["admin"])
        plan = planner.create_plan(old_active, self.candidate, scope="all")
        self.assertEqual(plan["expectedActiveStateSha256"], planner.canonical_hash(old_active))
        self.assertEqual(plan["desiredImages"]["LITELLM_IMAGE"], self.candidate["infrastructure"]["LITELLM_IMAGE"])

    def test_gateway_image_upgrade_requires_worker_selection(self):
        self.candidate["infrastructure"]["LITELLM_IMAGE"] = "docker.io/example/litellm@sha256:" + "f" * 64
        with patch.object(planner, "infer_compatibility", return_value={"databaseChange":"none", "contractChange":"compatible"}):
            admin = planner.create_plan(self.active, self.candidate, ["admin"], active_sources=self.sources)
            worker = planner.create_plan(self.active, self.candidate, ["worker"], active_sources=self.sources)
        self.assertEqual(admin["desiredImages"]["LITELLM_IMAGE"], self.active["LITELLM_IMAGE"])
        self.assertEqual(worker["desiredImages"]["LITELLM_IMAGE"], self.candidate["infrastructure"]["LITELLM_IMAGE"])

    def test_all_scope_selects_ten_components_and_go_scope_expands_to_five(self):
        with patch.object(planner, "infer_compatibility", return_value={"databaseChange":"none", "contractChange":"compatible"}):
            all_plan = planner.create_plan(self.active, self.candidate, scope="all", active_sources=self.sources)
            go_plan = planner.create_plan(self.active, self.candidate, ["video-worker"], active_sources=self.sources)
        self.assertEqual(set(all_plan["targets"]), set(planner.COMPONENTS))
        self.assertEqual(set(go_plan["targets"]), planner.GO_COMPONENTS)
        self.assertTrue(go_plan["operations"]["runMigration"])
        self.assertTrue(go_plan["operations"]["runSeed"])
        self.assertTrue(all_plan["operations"]["runMigration"])

    def test_changed_scope_associates_litellm_digest_change_with_worker(self):
        for component, key in planner.IMAGE_KEYS.items():
            self.candidate["components"][component]["image"] = self.active[key]
        self.candidate["infrastructure"]["LITELLM_IMAGE"] = "docker.io/example/litellm@sha256:" + "f" * 64
        with patch.object(planner, "infer_compatibility", return_value={"databaseChange":"none", "contractChange":"compatible"}):
            plan = planner.create_plan(self.active, self.candidate, scope="changed", active_sources=self.sources)
        self.assertEqual(set(plan["targets"]), planner.GO_COMPONENTS)
        self.assertEqual(plan["desiredImages"]["LITELLM_IMAGE"], self.candidate["infrastructure"]["LITELLM_IMAGE"])

    def test_changed_scope_keeps_unpublished_migration_and_contract_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = pathlib.Path(directory)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "Test"], check=True)
            def commit(name, content):
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)
                subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
                subprocess.run(["git", "-C", str(repo), "commit", "-qm", "change"], check=True)
                return subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
            origin = commit("deploy/oci/compose.yaml", "services: {}\n")
            commit("deploy/keycloak/realm.json", "{}\n")
            for name, value in (("Caddyfile", ":80 {}\n"), ("litellm-config.yaml", "model_list: []\n"), ("keycloak-realm.json", "{}\n")):
                commit("deploy/oci/" + name, value)
            commit("services/inspection/internal/platform/database/migrations/v77.sql", "migration")
            current = commit("services/inspection/schema.graphqls", "schema")
            candidate = dict(self.candidate, sourceSha=current)
            sources = {name: origin for name in planner.COMPONENTS}
            for name in set(planner.COMPONENTS) - planner.GO_COMPONENTS:
                candidate["components"][name]["image"] = self.active[planner.IMAGE_KEYS[name]]
                candidate["components"][name]["sourceSha"] = origin
            for name in planner.GO_COMPONENTS:
                candidate["components"][name]["sourceSha"] = current
            plan = planner.create_plan(self.active, candidate, scope="changed", active_sources=sources, repo=repo)
        self.assertEqual(set(plan["targets"]), set(planner.COMPONENTS))
        self.assertEqual(plan["compatibility"]["databaseChange"], "required")
        self.assertEqual(plan["compatibility"]["contractChange"], "review-required")
        self.assertEqual(plan["componentSources"]["admin"], origin)
        self.assertEqual(plan["componentSources"]["operations"], current)

    def test_video_receipt_upgrade_requires_all_components_and_plan_is_v2(self):
        old = {key: value for key, value in self.active.items() if key != "VIDEO_WORKER_IMAGE"}
        with self.assertRaisesRegex(ValueError, "complete ten-component"):
            planner.create_plan(old, self.candidate, ["admin"], active_sources=self.sources)
        plan = planner.create_plan(old, self.candidate, scope="all", active_sources=self.sources)
        self.assertEqual(plan["schemaVersion"], 2)
        self.assertEqual(plan["operation"], "deploy")


if __name__ == "__main__":
    unittest.main()
