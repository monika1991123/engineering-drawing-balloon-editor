#!/usr/bin/env python3
"""Regression cases for the longer leaders and solid blue arrowheads."""
import importlib.util
import json
import math
import sys
import unittest
from pathlib import Path
HERE = Path(__file__).resolve().parents[1]
import pymupdf as fitz

spec = importlib.util.spec_from_file_location("balloon_layout", HERE / "scripts" / "balloon.py")
b = importlib.util.module_from_spec(spec)
spec.loader.exec_module(b)

class ArrowAndSpacingTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.style = dict(b.DEFAULT)
        cls.style.update(json.loads((HERE / "style.json").read_text(encoding="utf-8")))

    def test_segment_rect_collision_detects_thin_text(self):
        rect = fitz.Rect(12, 12, 18, 19)
        self.assertTrue(b.segment_hits_rect(fitz.Point(0, 14), fitz.Point(30, 14), rect))
        self.assertFalse(b.segment_hits_rect(fitz.Point(0, 30), fitz.Point(30, 30), rect))
        self.assertTrue(b.line_hits_text(fitz.Point(0, 14), fitz.Point(30, 14),
                                         [rect], fitz.Rect(50, 50, 60, 60)))

    def test_arrow_points_and_target_gap(self):
        text_box = fitz.Rect(50, 50, 90, 65)
        center = fitz.Point(120, 30)
        tip = b.arrow_tip_outside_text(text_box, center, self.style["arrow_tip_gap"])
        self.assertFalse(text_box.contains(tip))
        start = fitz.Point(110, 35)
        triangle = b.arrow_points(start, tip, self.style)
        self.assertIsNotNone(triangle)
        self.assertAlmostEqual(math.hypot(triangle[0].x - triangle[3].x,
                                          triangle[0].y - triangle[3].y),
                               self.style["arrow_length"], places=3)

    def test_draws_filled_arrowhead(self):
        d = fitz.open()
        page = d.new_page(width=220, height=120)
        b.draw_arrow_head(page, fitz.Point(60, 30), fitz.Point(120, 65), self.style)
        drawing = page.get_drawings()
        self.assertTrue(any(item.get("fill") is not None for item in drawing))
        d.close()

    def test_auto_layout_is_spaced_and_does_not_hit_text(self):
        d = fitz.open()
        page = d.new_page(width=350, height=260)
        text = fitz.Rect(160, 110, 185, 125)
        features = [{"text":"78", "bbox":list(text), "kind":"dimension",
                     "source":"test", "confidence":1.0}]
        rows, review = b.layout(page, features, [text], self.style)
        self.assertFalse(review, review)
        self.assertEqual(len(rows), 1)
        row = rows[0]
        center = fitz.Point(row["center"])
        target = fitz.Point((text.x0 + text.x1)/2,(text.y0 + text.y1)/2)
        distance = math.hypot(center.x-target.x,center.y-target.y)
        self.assertGreaterEqual(distance + 0.01,
                                self.style["radius"]*self.style["min_distance_multiplier"])
        start, tip = (fitz.Point(point) for point in row["leader"])
        self.assertFalse(text.contains(tip))
        self.assertFalse(b.line_hits_text(start, tip, [text], text,
                                          self.style["leader_clearance"]))
        d.close()

    def test_balloons_keep_clear_of_vector_lines(self):
        d = fitz.open()
        page = d.new_page(width=350, height=260)
        # Thin lines next to the feature, where sparse pixel sampling used to miss them.
        for x in (110, 140, 205, 235):
            page.draw_line((x, 20), (x, 240), color=(0, 0, 0), width=0.3)
        text = fitz.Rect(160, 110, 185, 125)
        rows, review = b.layout(page, [{"text": "78", "bbox": list(text), "kind": "dimension",
                                        "source": "test", "confidence": 1.0}], [text], self.style)
        self.assertFalse(review, review)
        center = fitz.Point(rows[0]["center"])
        for x in (110, 140, 205, 235):
            self.assertGreaterEqual(abs(center.x - x), self.style["radius"] + self.style["line_clearance"])
        d.close()

    def test_keep_out_zone_is_respected(self):
        d = fitz.open()
        page = d.new_page(width=350, height=260)
        text = fitz.Rect(160, 110, 185, 125)
        zone = fitz.Rect(100, 20, 250, 110)  # everything above the feature
        rows, review = b.layout(page, [{"text": "78", "bbox": list(text), "kind": "dimension",
                                        "source": "test", "confidence": 1.0}], [text], self.style, [zone])
        self.assertFalse(review, review)
        self.assertFalse(rows[0]["circle"].intersects(zone))
        d.close()

    def test_dense_ordinate_row_is_fully_placed_in_order(self):
        d = fitz.open()
        page = d.new_page(width=500, height=300)
        boxes = [fitz.Rect(100 + 12 * i, 180, 108 + 12 * i, 200) for i in range(15)]
        features = [{"text": str(i), "bbox": list(r), "kind": "dimension",
                     "source": "test", "confidence": 1.0} for i, r in enumerate(boxes)]
        groups = b.find_dense_groups(features, self.style)
        self.assertEqual(len(groups), 1)
        self.assertEqual(len(groups[0]["members"]), 15)
        rows, review = b.layout(page, features, list(boxes), self.style)
        self.assertFalse(review, review)
        self.assertEqual([r["feature"]["text"] for r in rows], [str(i) for i in range(15)])
        r = self.style["radius"]
        for i, a in enumerate(rows):
            ca = fitz.Point(a["center"])
            self.assertLess(ca.y, 180)  # free side: above shared bottom edge
            for other in rows[i + 1:]:
                self.assertGreater(abs(ca - fitz.Point(other["center"])), 2 * r + 2)
        d.close()

    def test_spread_keeps_order_and_gap(self):
        pos = b.spread([0, 1, 2, 50, 51], 10)
        self.assertEqual(pos, sorted(pos))
        self.assertTrue(all(y - x >= 10 - 1e-9 for x, y in zip(pos, pos[1:])))
        self.assertAlmostEqual(pos[1], 1)  # tight cluster centred on its values

    def test_reading_order_top_left_to_bottom_right(self):
        def f(name, x, y):
            return {"text": name, "bbox": [x, y, x + 10, y + 8]}
        feats = [f("bottom-left", 20, 200), f("top-right", 300, 21), f("top-left", 20, 20),
                 f("middle", 150, 100)]
        rank = b.reading_order(feats, [], self.style)
        self.assertEqual([x["text"] for x in sorted(feats, key=lambda x: rank[id(x)])],
                         ["top-left", "top-right", "middle", "bottom-left"])

    def test_numbering_runs_view_by_view(self):
        def f(name, view, x, y):
            return {"text": name, "view": view, "bbox": [x, y, x + 10, y + 8]}
        # Views side by side: each view is numbered completely before the next one.
        feats = [f("L2", "left", 20, 300), f("R1", "right", 400, 15), f("L1", "left", 20, 40),
                 f("R2", "right", 420, 320), f("N1", "notes", 30, 700)]
        order = lambda vo=(): [x["text"] for x in sorted(
            feats, key=lambda x: b.reading_order(feats, [], self.style, 842, vo)[id(x)])]
        self.assertEqual(order(), ["L1", "L2", "R1", "R2", "N1"])
        self.assertEqual(order(["right"]), ["R1", "R2", "L1", "L2", "N1"])

    def test_merge_keeps_supplied_view_for_native_text(self):
        native = [{"text": "120", "bbox": [10, 10, 30, 20], "kind": "dimension", "source": "pdf_text"}]
        supplied = [{"text": "120", "bbox": [11, 10, 31, 21], "kind": "dimension", "source": "ai_ocr",
                     "view": "Main view"}]
        merged = b.merge_features(native, supplied)
        self.assertEqual(len(merged), 1)
        self.assertEqual(merged[0]["source"], "pdf_text")
        self.assertEqual(merged[0]["view"], "Main view")

    def test_wedge_leader_starts_on_rim_and_meets_at_tip(self):
        center, tip = fitz.Point(100, 100), fitz.Point(140, 130)
        left, right, point = b.wedge_points(center, tip, self.style)
        r = self.style["radius"]
        self.assertAlmostEqual(abs(left - center), r, places=6)
        self.assertAlmostEqual(abs(right - center), r, places=6)
        self.assertAlmostEqual(abs(left - right), self.style["wedge_width"], places=6)
        self.assertEqual(point, tip)
        d = fitz.open()
        page = d.new_page(width=220, height=200)
        b.draw_wedge(page, center, tip, self.style)
        self.assertTrue(page.get_drawings())
        d.close()

if __name__ == "__main__":
    unittest.main()
