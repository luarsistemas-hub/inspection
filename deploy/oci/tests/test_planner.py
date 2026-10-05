import importlib.util
import unittest
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
                          "compatibility": {"databaseChange": "none", "contractChange": "compatible"}}
        self.active = {key: f"ghcr.io/example/inspection-{name}@sha256:" + "d" * 64
                       for name, key in planner.IMAGE_KEYS.items()}
        self.active.update({key: f"docker.io/example/{key.lower()}@sha256:" + "e" * 64
                            for key in planner.INFRA_KEYS})

    def test_selective_update_preserves_other_active_images(self):
        self.candidate["components"]["admin"]["sourceSha"] = "a" * 40
        plan = planner.create_plan(self.active, self.candidate, ["admin"], "deploy-17")
        self.assertEqual(plan["desiredImages"]["ADMIN_IMAGE"], self.candidate["components"]["admin"]["image"])
        self.assertEqual(plan["desiredImages"]["WORKER_IMAGE"], self.active["WORKER_IMAGE"])
        self.assertFalse(plan["operations"]["runMigration"])
        self.assertEqual(plan["componentSources"]["admin"], "a" * 40)

    def test_database_change_requires_coordinated_processes(self):
        self.candidate["compatibility"] = {"databaseChange": "required", "contractChange": "compatible"}
        with self.assertRaisesRegex(ValueError, "coordinated selection"):
            planner.create_plan(self.active, self.candidate, ["api"])
        plan = planner.create_plan(self.active, self.candidate, ["api", "worker", "scheduler", "operations"])
        self.assertTrue(plan["operations"]["runMigration"])

    def test_first_deploy_requires_full_composition(self):
        with self.assertRaisesRegex(ValueError, "first deployment"):
            planner.create_plan({}, self.candidate, ["admin"])

    def test_missing_candidate_target_is_not_added_implicitly(self):
        del self.candidate["components"]["admin"]
        with self.assertRaisesRegex(ValueError, "does not contain selected"):
            planner.create_plan(self.active, self.candidate, ["admin"])

    def test_unknown_contract_impact_requires_full_selection(self):
        self.candidate["compatibility"]["contractChange"] = "unknown"
        with self.assertRaisesRegex(ValueError, "unverified contract change"):
            planner.create_plan(self.active, self.candidate, ["api"])

    def test_malformed_compatibility_metadata_is_rejected(self):
        self.candidate["compatibility"] = []
        with self.assertRaisesRegex(ValueError, "must be an object"):
            planner.create_plan(self.active, self.candidate, ["admin"])

    def test_rollback_changes_only_requested_component_and_never_migrates(self):
        previous = {key: value.replace("d" * 64, "f" * 64) for key, value in self.active.items()}
        plan = planner.create_rollback_plan(self.active, previous, ["worker"], "rollback-17")
        self.assertEqual(plan["desiredImages"]["WORKER_IMAGE"], previous["WORKER_IMAGE"])
        self.assertEqual(plan["desiredImages"]["API_IMAGE"], self.active["API_IMAGE"])
        self.assertFalse(plan["operations"]["runMigration"])


if __name__ == "__main__":
    unittest.main()
