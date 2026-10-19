import importlib.util
import pathlib
import unittest


SCRIPT = pathlib.Path(__file__).parents[1] / "ci" / "require_no_skips.py"
SPEC = importlib.util.spec_from_file_location("require_no_skips", SCRIPT)
require_no_skips = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(require_no_skips)


class RequireNoSkipsTests(unittest.TestCase):
    def test_passes_required_test(self):
        stream = ['{"Action":"run","Test":"TestRequired"}\n', '{"Action":"pass","Test":"TestRequired","Elapsed":0.1}\n']
        self.assertEqual(require_no_skips.check_stream(stream, {"TestRequired"}), [])

    def test_rejects_skipped_required_test(self):
        stream = ['{"Action":"skip","Test":"TestRequired","Output":"missing env"}\n']
        self.assertEqual(require_no_skips.check_stream(stream, {"TestRequired"}), ["required test was skipped: TestRequired"])

    def test_rejects_missing_required_test(self):
        self.assertEqual(require_no_skips.check_stream([], {"TestRequired"}), ["required test did not execute: TestRequired"])

    def test_ignores_optional_skips_and_rejects_malformed_json(self):
        stream = ['{"Action":"skip","Test":"OptionalTest"}\n', '{broken}\n']
        errors = require_no_skips.check_stream(stream, {"TestRequired"})
        self.assertEqual(len(errors), 2)
        self.assertIn("required test did not execute: TestRequired", errors)
        self.assertTrue(any(error.startswith("line 2: invalid go test JSON:") for error in errors))


if __name__ == "__main__":
    unittest.main()
