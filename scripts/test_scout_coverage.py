import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('coverage', Path(__file__).with_name('scout-coverage.py'))
coverage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(coverage)


class GeometryTest(unittest.TestCase):
    def test_holes_and_disjoint_rectangles(self):
        exterior = [(0, 0), (10, 0), (10, 10), (0, 10), (0, 0)]
        hole = [(3, 3), (7, 3), (7, 7), (3, 7), (3, 3)]
        polygon = ((0, 0, 10, 10), [exterior, hole])
        self.assertTrue(coverage.contains(polygon, (1, 1)))
        self.assertFalse(coverage.contains(polygon, (5, 5)))
        self.assertFalse(coverage.intersects(polygon, (4, 4, 6, 6)))
        self.assertTrue(coverage.intersects(polygon, (2, 2, 4, 4)))
        self.assertFalse(coverage.intersects(polygon, (11, 0, 12, 1)))

    def test_crossing_without_vertices_or_corners_inside(self):
        polygon = ((-5, -.1, 5, .1), [[(-5, -.1), (5, -.1), (5, .1), (-5, .1), (-5, -.1)]])
        self.assertTrue(coverage.intersects(polygon, (-1, -1, 1, 1)))
        self.assertFalse(coverage.intersects(polygon, (-1, 2, 1, 3)))


if __name__ == '__main__':
    unittest.main()
