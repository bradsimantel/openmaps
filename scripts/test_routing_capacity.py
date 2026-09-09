"""Deterministic tests for capacity decisions, independent of the desktop host."""
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("capacity", Path(__file__).with_name("routing-capacity.py"))
capacity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capacity)


class CapacityTests(unittest.TestCase):
    def setUp(self):
        row = dict(name="fixture", evidence="synthetic", source_bytes=10)
        row.update({key: 10 for key in capacity.METRICS})
        self.model = dict(schema=1, contingency=1.5, baselines=[row],
                          sources=[dict(name="target", source_bytes=100, evidence="synthetic")])
        self.host = dict(ram_bytes=10000, free_disk_bytes=10000)
        self.budgets = dict(construction_bytes=200, disk_reserve_bytes=100)

    def run_model(self):
        return capacity.estimate(self.model, self.host, self.budgets)["results"][0]

    def test_overlap_and_contingency(self):
        result = self.run_model()
        self.assertEqual(result["construction_planning_bytes"], 150)
        self.assertEqual(result["disk_phases_bytes"], {"acquisition": 100, "import": 400,
                         "first_publication": 550, "rebuild_and_old_new_overlap": 850})
        self.assertEqual(result["decision"], "fits_estimate_only")

    def test_disk_boundary_and_memory_are_independent(self):
        self.host["free_disk_bytes"] = 950
        self.assertEqual(self.run_model()["decision"], "fits_estimate_only")
        self.host["free_disk_bytes"] -= 1
        self.assertEqual(len(self.run_model()["reasons"]), 1)
        self.budgets["construction_bytes"] = 149
        self.assertEqual(len(self.run_model()["reasons"]), 2)

    def test_worst_ratio_is_order_independent(self):
        other = copy.deepcopy(self.model["baselines"][0])
        other.update(name="larger", import_footprint_bytes=20)
        self.model["baselines"].append(other)
        first = self.run_model()
        self.model["baselines"].reverse()
        self.assertEqual(first, self.run_model())
        self.assertEqual(first["construction_planning_bytes"], 300)

    def test_invalid_measurements_fail(self):
        for value in (0, -1, float("inf"), float("nan"), True, "10"):
            with self.subTest(value=value):
                self.model["baselines"][0]["source_bytes"] = value
                with self.assertRaises(ValueError):
                    self.run_model()

    def test_duplicates_missing_evidence_and_versions_fail(self):
        original = copy.deepcopy(self.model)
        for mutation in (lambda m: m["baselines"].append(copy.deepcopy(m["baselines"][0])),
                         lambda m: m["sources"].append(copy.deepcopy(m["sources"][0])),
                         lambda m: m["baselines"][0].pop("evidence"),
                         lambda m: m.update(schema=2),
                         lambda m: m.update(contingency=.5)):
            self.model = copy.deepcopy(original)
            mutation(self.model)
            with self.assertRaises(ValueError):
                self.run_model()

    def test_representation_and_arithmetic_overflow(self):
        self.model["baselines"][0]["segments"] = 1000000000
        self.assertIn("projected segments exceeds current representation limit", self.run_model()["reasons"])
        self.model["sources"][0]["source_bytes"] = 1e308
        with self.assertRaises(ValueError):
            self.run_model()


if __name__ == "__main__":
    unittest.main()
