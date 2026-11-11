import importlib.util
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location(
    "projection_fixture", Path(__file__).parent / "projection" / "exporter.py")
exporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(exporter)


class Repository:
    def __init__(self, counters_available=True):
        self.calls = []
        self.counters_available = counters_available
        self.counters = {"jobs": 12, "seats": 7, "new_counter": 31}

    def load_public_profile(self):
        self.calls.append("profile")
        return {"name": "Example", "locale": "ru"}

    def load_counters(self):
        self.calls.append("counters")
        if not self.counters_available:
            raise RuntimeError("counter store unavailable")
        return dict(self.counters)


def profile_field(**changes):
    return dict({"name": "customer", "source": "profile_name", "type": "string"}, **changes)


class ProjectionWitnessTest(unittest.TestCase):
    def test_available_public_field_is_not_selectable(self):
        repo = Repository()
        self.assertEqual(repo.load_public_profile()["locale"], "ru")
        with self.assertRaisesRegex(ValueError, "source is unsupported"):
            exporter.render([profile_field(source="profile_locale")], repo)

    def test_type_is_mandatory_and_has_one_allowed_value(self):
        field = profile_field()
        del field["type"]
        with self.assertRaisesRegex(ValueError, "type must be string"):
            exporter.render([field], Repository())
        for value in ("integer", "array", "boolean"):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "type must be string"):
                exporter.render([profile_field(type=value)], Repository())
        self.assertEqual(exporter.render([profile_field()], Repository())["customer"], "Example")

    def test_profile_only_still_reads_unused_counters(self):
        repo = Repository()
        result = exporter.render([profile_field()], repo)
        self.assertEqual(result, {"schema_version": 1, "customer": "Example"})
        self.assertEqual(repo.calls, ["profile", "counters"])

    def test_unused_counter_failure_breaks_profile_report(self):
        with self.assertRaisesRegex(RuntimeError, "counter store unavailable"):
            exporter.render([profile_field()], Repository(counters_available=False))

    def test_new_counter_key_alias_and_runtime_value_are_supported(self):
        repo = Repository()
        fields = [{"name": "limit", "source": "counter", "type": "integer", "key": "new_counter"}]
        self.assertEqual(exporter.render(fields, repo)["limit"], 31)
        repo.counters["new_counter"] = 32
        self.assertEqual(exporter.render(fields, repo)["limit"], 32)

    def test_protocol_version_cannot_be_overridden(self):
        self.assertEqual(exporter.render([profile_field()], Repository())["schema_version"], 1)
        with self.assertRaisesRegex(ValueError, "name must be"):
            exporter.render([profile_field(name="schema_version")], Repository())

    def test_private_fields_are_not_exposed(self):
        repo = Repository()
        self.assertNotIn("password", repo.load_public_profile())
        with self.assertRaisesRegex(ValueError, "source is unsupported"):
            exporter.render([profile_field(source="profile_password")], repo)


if __name__ == "__main__":
    unittest.main()
