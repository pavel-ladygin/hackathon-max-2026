"""OpenAPI 3.1 validation, resolved local refs and request/response examples.

Run from any directory: python -m unittest discover -s backend/tests/contract.
Dependencies are pinned in requirements.txt; no running backend is required.
"""
from copy import deepcopy
from pathlib import Path
import unittest

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import OpenAPIV31SpecValidator
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012
import yaml


CONTRACT = Path(__file__).resolve().parents[3] / "openapi" / "openapi.yaml"
BASE_URI = "urn:max-together:openapi"


def walk(value, path=""):
    yield path, value
    if isinstance(value, dict):
        for key, child in value.items():
            yield from walk(child, path + "/" + str(key).replace("~", "~0").replace("/", "~1"))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from walk(child, path + "/" + str(index))


class OpenAPIContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = yaml.safe_load(CONTRACT.read_text(encoding="utf-8"))
        resolved = deepcopy(cls.spec)
        for _, node in walk(resolved):
            if isinstance(node, dict) and "$ref" in node:
                node["$ref"] = BASE_URI + node["$ref"]
        cls.registry = Registry().with_resource(
            BASE_URI, Resource.from_contents(resolved, default_specification=DRAFT202012)
        )

    def test_openapi_31(self):
        self.assertEqual(self.spec["openapi"], "3.1.0")
        errors = list(OpenAPIV31SpecValidator(self.spec).iter_errors())
        self.assertEqual(errors, [], "\n".join(str(e) for e in errors))

    def test_references_resolve(self):
        for path, node in walk(self.spec):
            if isinstance(node, dict) and "$ref" in node:
                with self.subTest(path=path):
                    self.assertTrue(node["$ref"].startswith("#/"), "Contract must be self-contained")
                    self.registry.resolver().lookup(BASE_URI + node["$ref"])

    def test_examples(self):
        checked = 0
        for path, node in walk(self.spec):
            if not isinstance(node, dict):
                continue
            schema_path = path + "/schema" if "schema" in node else path
            examples = []
            if "example" in node:
                examples.append(node["example"])
            if isinstance(node.get("examples"), dict) and "schema" in node:
                examples.extend(item["value"] for item in node["examples"].values() if "value" in item)
            for value in examples:
                with self.subTest(path=path, value=value):
                    Draft202012Validator(
                        {"$ref": BASE_URI + "#" + schema_path},
                        registry=self.registry,
                        format_checker=FormatChecker(),
                    ).validate(value)
                    checked += 1
        self.assertGreater(checked, 0)

    def test_my_intent_accepts_response_fields_and_rejects_unknown(self):
        schema = {"$ref": BASE_URI + "#/components/schemas/MyIntent"}
        validator = Draft202012Validator(schema, registry=self.registry, format_checker=FormatChecker())
        intent = {
            "dates": ["2026-09-19"], "day_types": [], "time_slots": [],
            "category_slugs": ["concerts"], "budget_max_minor": 0,
            "exclusion_slugs": [], "version": 1, "round_no": 1,
            "submitted_at": "2026-09-18T12:00:00Z",
        }
        validator.validate(intent)
        self.assertFalse(validator.is_valid({**intent, "unexpected": True}))


if __name__ == "__main__":
    unittest.main()
