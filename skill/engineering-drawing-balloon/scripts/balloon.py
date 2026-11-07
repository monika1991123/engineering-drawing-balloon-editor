#!/usr/bin/env python3
"""Deterministic, review-first PDF inspection ballooner.
Copyright (C) the engineering-drawing-balloon contributors. Licensed under AGPL-3.0-only.
Requires PyMuPDF. Native PDF text or an AI/OCR feature JSON can be used.
No drawing content is replaced; balloons are vector overlays.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import json
import math
import re
import sys
import zipfile
from pathlib import Path
from xml.sax.saxutils import escape

try:
    import pymupdf as fitz
except ImportError:
    sys.exit("PyMuPDF is required: run scripts/setup.sh or pip install -r requirements.txt")

BLUE = (0.10, 0.36, 0.78)
DIM_RE = re.compile(
    r"(?ix)^(?:[0-9]{1,2}\s*[-x×]\s*)?(?:[⌀ØøΦφ]\s*)?"
    r"\d+(?:[.,]\d+)?(?:\s*[±]\s*\d+(?:[.,]\d+)?)?"
    r"(?:\s*[°])?$"
)
OTHER_RE = re.compile(r"(?i)^(?:Rz|Ra)\s*\d+(?:[.,]\d+)?$|^[Rr]\s*\d+(?:[.,]\d+)?$|^M\d+(?:[.,]\d+)?$")
SAFE_TEXT = re.compile(r"(?i)^[\s\d.,±+−\-x×()⌀ØøΦφ°RrMmZzAa/]+$")
DEFAULT = {"radius": 9.4, "font_size": 7.8, "line_width": 0.85,
           "color": [0.33, 0.49, 0.68], "page_margin": 11,
           "min_ink_clearance": 2.4, "title_block_bottom": 0.81,
           "title_block_right": 0.52, "top_grid_ignore": 0.085,
           "left_grid_ignore": 0.045, "arrow_length": 5.0,
           "arrow_width": 2.8, "arrow_tip_gap": 2.4,
           "leader_clearance": 1.0, "min_distance_multiplier": 3.55,
           "distance_multipliers": [3.55, 4.35, 5.3, 6.4, 7.8, 9.3, 11.2, 13.5, 15.5, 18.0],
           "line_clearance": 1.5, "max_ink_ratio": 0.012,
           "leader_crossing_penalty": 0.9, "group_align_tolerance": 3.0,
           "order_row_tolerance": 11.0, "order_block_tolerance": 0.1,
           "leader_style": "wedge", "wedge_width": 3.6}
DIRECTIONS = [(-1,-1.0),(1,-1.0),(-1,-0.55),(1,-0.55),
              (-1,0.2),(1,0.2),(0,-1.15),(-0.55,-1),(0.55,-1),
              (-1,1),(1,1),(0,1),(-0.3,1),(0.3,1)]
DARK = bytes(1 if i < 214 else 0 for i in range(256))

def ensure_pdf(path: Path):
    if not path.exists():
        raise ValueError("FILE_MISSING: " + str(path))
    raw = path.read_bytes()
    if not raw.startswith(b"%PDF-") or b"%%EOF" not in raw[-2048:]:
        raise ValueError("INCOMPLETE_PDF: missing PDF header or final %%EOF: " + str(path))
    doc = fitz.open(str(path))
    if doc.page_count < 1 or doc.needs_pass:
        doc.close()
        raise ValueError("UNREADABLE_PDF: no pages or password required: " + str(path))
    return doc

def classify(s: str):
    s = re.sub(r"\s+", "", s.strip())
    if not s or len(s) > 23:
        return None
    if OTHER_RE.fullmatch(s):
        return "roughness" if s.lower().startswith(("ra", "rz")) else ("radius" if s.lower().startswith("r") else "thread")
    if DIM_RE.fullmatch(s):
        if s.count(".") >= 2 or s.count(",") >= 2:
            return None
        if s.startswith(("Ø", "⌀", "ø", "Φ", "φ")) or "Ø" in s or "⌀" in s:
            return "diameter"
        if "±" in s: return "tolerance"
        if s.endswith("°"): return "angle"
        return "dimension"
    return None

def candidate_text(page, config):
    result, protected, seen = [], [], set()
    data = page.get_text("dict")
    for block in data["blocks"]:
        if block.get("type") != 0: continue
        for line in block.get("lines", []):
            for span in line.get("spans", []):
                s = span.get("text", "").strip()
                if not s: continue
                rect = fitz.Rect(span["bbox"])
                protected.append(rect)
                kind = classify(s)
                if kind is None: continue
                x, y = (rect.x0 + rect.x1) / 2, (rect.y0 + rect.y1) / 2
                w, h = page.rect.width, page.rect.height
                if y < h * config["top_grid_ignore"] or x < w * config["left_grid_ignore"]:
                    continue
                if y > h * config["title_block_bottom"] and x > w * config["title_block_right"]:
                    continue
                key = (s, round(x, 1), round(y, 1))
                if key in seen: continue
                seen.add(key)
                result.append({"text": s, "kind": kind, "bbox": list(rect),
                               "source": "pdf_text", "confidence": None})
    # Also protect every word, including non-dimension labels / title blocks.
    for w in page.get_text("words"):
        protected.append(fitz.Rect(*w[:4]))
    return result, protected

def features_from_json(path: Path, page_count: int):
    raw = json.loads(path.read_text(encoding="utf-8"))
    if isinstance(raw, dict): raw = raw.get("features", [])
    if not isinstance(raw, list): raise ValueError("features JSON must be a list")
    grouped = {p: [] for p in range(page_count)}
    for i, row in enumerate(raw):
        if not isinstance(row, dict): raise ValueError("feature must be an object")
        page = int(row.get("page", 1)) - 1
        if page not in grouped: raise ValueError("feature page out of range at index " + str(i))
        bbox = row.get("bbox")
        if not isinstance(bbox, list) or len(bbox) != 4: raise ValueError("feature bbox must be [x0,y0,x1,y1] PDF points")
        rect = fitz.Rect(*map(float, bbox))
        if rect.is_empty or rect.is_infinite: raise ValueError("invalid bbox at index " + str(i))
        label = str(row.get("text", "")).strip()
        if not label: raise ValueError("empty feature text at index " + str(i))
        confidence = row.get("confidence")
        if confidence is not None and not 0 <= float(confidence) <= 1:
            raise ValueError("confidence outside [0,1]")
        grouped[page].append({"text": label, "bbox": list(rect),
                              "kind": row.get("kind") or classify(label) or "other",
                              "source": row.get("source", "ai_ocr"),
                              "confidence": confidence,
                              "view": str(row.get("view", "")).strip()})
    return grouped

def merge_features(a, b):
    result = list(a)
    for item in b:
        rect = fitz.Rect(item["bbox"])
        same = next((old for old in result
                     if item["text"].replace(" ", "") == old["text"].replace(" ", "")
                     and abs((fitz.Rect(old["bbox"]).tl + fitz.Rect(old["bbox"]).br).x / 2 -
                             (rect.tl + rect.br).x / 2) < 6
                     and abs((fitz.Rect(old["bbox"]).tl + fitz.Rect(old["bbox"]).br).y / 2 -
                             (rect.tl + rect.br).y / 2) < 6), None)
        if same is not None:
            # Keep the native text geometry, but adopt the supplied view assignment.
            if item.get("view") and not same.get("view"):
                same["view"] = item["view"]
            continue
        result.append(item)
    return sorted(result, key=lambda a: (round(fitz.Rect(a["bbox"]).y0 / 18), fitz.Rect(a["bbox"]).x0))

def keep_out_from_json(path: Path, page_count: int):
    """Optional {"keep_out": [{"page": 1, "bbox": [...]}]} zones where no balloon may sit."""
    raw = json.loads(path.read_text(encoding="utf-8"))
    zones = {p: [] for p in range(page_count)}
    for i, row in enumerate(raw.get("keep_out", []) if isinstance(raw, dict) else []):
        page = int(row.get("page", 1)) - 1
        bbox = row.get("bbox")
        if page not in zones or not isinstance(bbox, list) or len(bbox) != 4:
            raise ValueError("invalid keep_out zone at index " + str(i))
        zones[page].append(fitz.Rect(*map(float, bbox)))
    return zones

def intersects(a, b, margin=0):
    return bool(fitz.Rect(a.x0 - margin, a.y0 - margin, a.x1 + margin, a.y1 + margin).intersects(b))

def inside_page(circle, bounds, pad):
    return circle.x0 >= bounds.x0 + pad and circle.y0 >= bounds.y0 + pad and circle.x1 <= bounds.x1 - pad and circle.y1 <= bounds.y1 - pad

def page_raster(page):
    bounds = page.rect
    scale = max(0.6, min(1.5, 2700 / max(bounds.width, bounds.height)))
    pix = page.get_pixmap(matrix=fitz.Matrix(scale, scale), colorspace=fitz.csGRAY, alpha=False)
    return {"samples": pix.samples, "stride": pix.stride, "width": pix.width,
            "height": pix.height, "scale": scale}

def ink_ratio(raster, cx, cy, radius):
    """Dense coverage test: share of dark pixels inside a proposed balloon disc."""
    s = raster["scale"]
    rs, px, py = radius * s, cx * s, cy * s
    if px - rs < 0 or py - rs < 0 or px + rs >= raster["width"] or py + rs >= raster["height"]:
        return 1.0
    dark = total = 0
    for y in range(int(py - rs), int(py + rs) + 1):
        dy = y + 0.5 - py
        if abs(dy) > rs:
            continue
        half = math.sqrt(rs * rs - dy * dy)
        row_start = y * raster["stride"]
        row = raster["samples"][row_start + int(px - half): row_start + int(px + half) + 1]
        total += len(row)
        dark += row.translate(DARK).count(1)
    return dark / max(total, 1)

def point_segment_distance(p, seg):
    x0, y0, x1, y1 = seg
    dx, dy = x1 - x0, y1 - y0
    length2 = dx * dx + dy * dy
    t = 0.0 if length2 < 1e-12 else max(0.0, min(1.0, ((p.x - x0) * dx + (p.y - y0) * dy) / length2))
    return math.hypot(p.x - (x0 + t * dx), p.y - (y0 + t * dy))

def drawing_segments(page):
    """Vector line work (lines, rectangles, quads, flattened Béziers) of a page."""
    segments = []
    for path in page.get_drawings():
        for item in path["items"]:
            op = item[0]
            if op == "l":
                points = [item[1], item[2]]
            elif op == "re":
                r = item[1]
                points = [r.tl, r.tr, r.br, r.bl, r.tl]
            elif op == "qu":
                q = item[1]
                points = [q.ul, q.ur, q.lr, q.ll, q.ul]
            elif op == "c":
                p0, c1, c2, p3 = item[1:5]
                points = []
                for t in (0, 0.25, 0.5, 0.75, 1):
                    u = 1 - t
                    points.append(fitz.Point(
                        u**3 * p0.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t**3 * p3.x,
                        u**3 * p0.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t**3 * p3.y))
            else:
                continue
            segments += [(a.x, a.y, b.x, b.y) for a, b in zip(points, points[1:])]
    return segments

class SegmentIndex:
    """Grid-bucketed vector segments for fast balloon clearance and leader crossing tests."""
    def __init__(self, segments, cell=16.0):
        self.segments, self.cell, self.grid = segments, cell, {}
        for i, (x0, y0, x1, y1) in enumerate(segments):
            for key in self._cells(min(x0, x1), min(y0, y1), max(x0, x1), max(y0, y1)):
                self.grid.setdefault(key, []).append(i)

    def _cells(self, x0, y0, x1, y1):
        c = self.cell
        for i in range(int(math.floor(x0 / c)), int(math.floor(x1 / c)) + 1):
            for j in range(int(math.floor(y0 / c)), int(math.floor(y1 / c)) + 1):
                yield i, j

    def _near(self, x0, y0, x1, y1):
        seen = set()
        for key in self._cells(x0, y0, x1, y1):
            for i in self.grid.get(key, ()):
                if i not in seen:
                    seen.add(i)
                    yield self.segments[i]

    def hits_circle(self, center, radius):
        return any(point_segment_distance(center, seg) < radius for seg in
                   self._near(center.x - radius, center.y - radius, center.x + radius, center.y + radius))

    def crossings(self, start, end):
        return sum(segment_intersect(start, end, fitz.Point(x0, y0), fitz.Point(x1, y1))
                   for x0, y0, x1, y1 in self._near(min(start.x, end.x), min(start.y, end.y),
                                                    max(start.x, end.x), max(start.y, end.y)))

def segment_hits_rect(start, end, rect, clearance=0.0):
    """Liang-Barsky line clipping: true if a segment enters a protected rectangle."""
    box = fitz.Rect(rect.x0 - clearance, rect.y0 - clearance,
                    rect.x1 + clearance, rect.y1 + clearance)
    dx, dy = end.x - start.x, end.y - start.y
    t0, t1 = 0.0, 1.0
    for p, q in ((-dx, start.x - box.x0), (dx, box.x1 - start.x),
                 (-dy, start.y - box.y0), (dy, box.y1 - start.y)):
        if abs(p) < 1e-10:
            if q < 0:
                return False
            continue
        t = q / p
        if p < 0:
            t0 = max(t0, t)
        else:
            t1 = min(t1, t)
        if t0 > t1:
            return False
    return True

def arrow_tip_outside_text(rect, center, clearance):
    """Aim at a dimension's nearest bounding-box edge, not through its glyphs."""
    target = fitz.Point((rect.x0 + rect.x1) / 2, (rect.y0 + rect.y1) / 2)
    dx, dy = center.x - target.x, center.y - target.y
    length = math.hypot(dx, dy)
    if length < 1e-9:
        return target
    ux, uy = dx / length, dy / length
    candidates = []
    if abs(ux) > 1e-8:
        candidates.append(((rect.x1 if ux > 0 else rect.x0) - target.x) / ux)
    if abs(uy) > 1e-8:
        candidates.append(((rect.y1 if uy > 0 else rect.y0) - target.y) / uy)
    distance = min(x for x in candidates if x >= 0) + clearance
    return fitz.Point(target.x + ux * distance, target.y + uy * distance)

def arrow_points(start, tip, style):
    """A short, filled triangle with its point directed towards the dimension."""
    dx, dy = tip.x - start.x, tip.y - start.y
    length = math.hypot(dx, dy)
    if length < 1e-8:
        return None
    ux, uy = dx / length, dy / length
    alen = min(float(style.get("arrow_length", 5.0)), length * 0.45)
    awidth = float(style.get("arrow_width", 2.8))
    back = fitz.Point(tip.x - ux * alen, tip.y - uy * alen)
    left = fitz.Point(back.x - uy * awidth / 2, back.y + ux * awidth / 2)
    right = fitz.Point(back.x + uy * awidth / 2, back.y - ux * awidth / 2)
    return tip, left, right, back

def wedge_points(center, tip, style):
    """Tapered leader: two points on the balloon rim converging on the tip."""
    radius = float(style["radius"])
    dx, dy = tip.x - center.x, tip.y - center.y
    length = math.hypot(dx, dy)
    if length < 1e-8:
        return None
    ux, uy = dx / length, dy / length
    half = min(float(style["wedge_width"]) / 2, radius * 0.9)
    cos_t = math.sqrt(radius * radius - half * half) / radius
    sin_t = half / radius
    left = fitz.Point(center.x + radius * (ux * cos_t - uy * sin_t),
                      center.y + radius * (uy * cos_t + ux * sin_t))
    right = fitz.Point(center.x + radius * (ux * cos_t + uy * sin_t),
                       center.y + radius * (uy * cos_t - ux * sin_t))
    return left, right, tip

def leader_footprint(center, start, end, style):
    """Segments a leader occupies for the configured leader_style (wedge or arrow)."""
    if style.get("leader_style", "wedge") == "wedge":
        points = wedge_points(center, end, style)
        if points is None:
            return None
        left, right, tip = points
        return [(left, tip), (right, tip)]
    triangle = arrow_points(start, end, style)
    if triangle is None:
        return None
    tip, left, right, back = triangle
    return [(start, end), (left, tip), (right, tip)]

def line_hits_text(start, end, protected, own_rect, clearance=1.0):
    for rect in protected:
        # The arrow is designed to stop just outside its OWN dimension text.
        if rect == own_rect:
            continue
        if segment_hits_rect(start, end, rect, clearance):
            return True
    return False

def segment_intersect(a, b, c, d):
    def cross(x, y, z):
        return (y.x - x.x) * (z.y - x.y) - (y.y - x.y) * (z.x - x.x)
    ab1, ab2 = cross(a,b,c), cross(a,b,d)
    cd1, cd2 = cross(c,d,a), cross(c,d,b)
    return ab1 * ab2 < 0 and cd1 * cd2 < 0

def segment_hits_balloon(start, end, center, radius, clearance):
    """True circle test; a padded bounding square would close corridors between balloons."""
    return point_segment_distance(center, (start.x, start.y, end.x, end.y)) < radius + clearance

def feature_center(feature):
    b = feature["bbox"]
    return ((b[0] + b[2]) / 2, (b[1] + b[3]) / 2)

def evaluate(center, own, ctx, tip=None):
    """Check one balloon position against every obstacle; None if it is not usable.
    `tip` fixes the arrow point; by default it aims at the nearest edge of `own`."""
    style, radius = ctx["style"], ctx["radius"]
    circle = fitz.Rect(center.x - radius - 1.3, center.y - radius - 1.3,
                       center.x + radius + 1.3, center.y + radius + 1.3)
    if not inside_page(circle, ctx["bounds"], style["page_margin"]):
        return None
    if any(circle.intersects(zone) for zone in ctx["keep_out"]):
        return None
    if any(intersects(circle, pr, style["min_ink_clearance"]) for pr in ctx["protected"]):
        return None
    if any(intersects(circle, existing["circle"], 2.0) for existing in ctx["placed"]):
        return None
    # A new balloon may not cover an existing balloon's leader line.
    if any(segment_hits_balloon(a, b, center, radius, 1.1) for a, b in ctx["leaders"]):
        return None
    # Vector line work (frames, outlines, dimension lines, outlined glyphs).
    if ctx["segments"].hits_circle(center, radius + float(style["line_clearance"])):
        return None
    ink = ink_ratio(ctx["raster"], center.x, center.y, radius + 1.3)
    if ink > float(style["max_ink_ratio"]):
        return None
    end = tip or arrow_tip_outside_text(own, center, float(style["arrow_tip_gap"]))
    vec = fitz.Point(end.x - center.x, end.y - center.y)
    length = math.hypot(vec.x, vec.y)
    if length < radius + float(style["arrow_length"]) + 2:
        return None
    start = fitz.Point(center.x + vec.x * radius / length, center.y + vec.y * radius / length)
    footprint = leader_footprint(center, start, end, style)
    if footprint is None:
        return None
    clearance = float(style["leader_clearance"])
    # Include the leader's entire footprint, not just its centre line.
    if any(line_hits_text(a, b, ctx["protected"], own, clearance) for a, b in footprint):
        return None
    if style.get("leader_style", "wedge") != "wedge":
        tip, left, right, back = arrow_points(start, end, style)
        if segment_hits_rect(left, right, own, 0.1):
            return None
    if any(segment_hits_balloon(start, end, fitz.Point(existing["center"]), radius, clearance)
           for existing in ctx["placed"]):
        return None
    if any(segment_intersect(start, end, a, b) for a, b in ctx["leaders"]):
        return None
    return {"center": center, "circle": circle, "start": start, "end": end,
            "ink": ink, "crossings": ctx["segments"].crossings(start, end)}

def commit(ctx, feature, candidate):
    ctx["placed"].append({"feature": feature, "center": list(candidate["center"]),
                          "circle": candidate["circle"],
                          "leader": [list(candidate["start"]), list(candidate["end"])]})
    ctx["leaders"].append((candidate["start"], candidate["end"]))

def place_greedy(feature, ctx):
    style, radius = ctx["style"], ctx["radius"]
    own = fitz.Rect(feature["bbox"])
    tx, ty = feature_center(feature)
    minimum = float(style["min_distance_multiplier"])
    best = None
    for mult in style["distance_multipliers"]:
        if mult < minimum:
            continue
        for dx, dy in DIRECTIONS:
            norm = math.hypot(dx, dy)
            center = fitz.Point(tx + dx / norm * radius * mult, ty + dy / norm * radius * mult)
            cand = evaluate(center, own, ctx)
            if cand is None:
                continue
            score = (abs(mult - 4.2) * 0.60 + mult * 0.25
                     + 55 * cand["ink"] + (2.0 if dy > 0 else 0)
                     + (0.35 if dx == 0 else 0)
                     + float(style["leader_crossing_penalty"]) * cand["crossings"])
            if best is None or score < best[0]:
                best = (score, cand)
    return best[1] if best else None

def balloon_gap(style):
    return 2 * (float(style["radius"]) + 1.3) + 2.5

def find_dense_groups(features, style):
    """Rows/columns of features sharing an edge and packed tighter than one balloon,
    e.g. ordinate dimension stacks. Greedy placement cannot fit these one by one."""
    gap, tol = balloon_gap(style), float(style["group_align_tolerance"])
    groups, used = [], set()
    for axis, edges in (("x", (3, 1)), ("y", (2, 0))):
        along = 0 if axis == "x" else 1
        for edge in edges:
            pool = sorted((f for f in features if id(f) not in used), key=lambda f: f["bbox"][edge])
            buckets = []
            for f in pool:
                if buckets and f["bbox"][edge] - buckets[-1][0]["bbox"][edge] <= tol:
                    buckets[-1].append(f)
                else:
                    buckets.append([f])
            for bucket in buckets:
                bucket.sort(key=lambda f: feature_center(f)[along])
                chain = []
                for f in bucket + [None]:
                    if f is not None and (not chain or feature_center(f)[along]
                                          - feature_center(chain[-1])[along] <= 2.5 * gap):
                        chain.append(f)
                        continue
                    spacing = [feature_center(b)[along] - feature_center(a)[along]
                               for a, b in zip(chain, chain[1:])]
                    if len(chain) >= 3 and min(spacing) < gap:
                        groups.append({"axis": axis, "edge": edge, "members": chain})
                        used.update(id(m) for m in chain)
                    chain = [f] if f is not None else []
    return groups

def spread(values, gap):
    """Least-squares positions closest to sorted `values` with neighbours >= gap apart."""
    blocks = []  # [sum of (value - index * gap), count]
    for v in values:
        blocks.append([v, 1])
        while len(blocks) > 1:
            (s0, n0), (s1, n1) = blocks[-2], blocks[-1]
            if s0 / n0 + n0 * gap <= s1 / n1 + 1e-9:
                break
            blocks[-2:] = [[s0 + s1 - n0 * n1 * gap, n0 + n1]]
    return [s / n + i * gap for s, n in blocks for i in range(n)]

def place_group(group, ctx):
    """Staggered lanes of balloons on the free side of a dense group.
    Returns the members that could not be placed, for greedy fallback."""
    style, radius = ctx["style"], ctx["radius"]
    members = group["members"]
    along = 0 if group["axis"] == "x" else 1
    cross = 1 - along
    # Shared bottom/right edge -> balloons above/left; shared top/left -> below/right.
    sign = -1 if group["edge"] in (3, 2) else 1
    boxes = [f["bbox"] for f in members]
    centres = [feature_center(f) for f in members]
    need = radius + float(style["arrow_length"]) + float(style["arrow_tip_gap"]) + 4
    minimum = float(style["min_distance_multiplier"]) * radius
    if sign < 0:
        lane0 = min(min(b[cross] for b in boxes) - need, min(c[cross] for c in centres) - minimum)
    else:
        lane0 = max(max(b[cross + 2] for b in boxes) + need, max(c[cross] for c in centres) + minimum)
    gap, pitch = balloon_gap(style), 2 * radius + 8
    tip_gap = float(style["arrow_tip_gap"])
    span = centres[-1][along] - centres[0][along]
    # One joint 1-D spread with alternating lanes: an outer balloon sits midway between
    # two inner ones, so its leader runs through the corridor they leave open.
    def step_for(k):
        return gap if k == 1 else gap * 1.2 / k
    lanes = next((k for k in (1, 2, 3) if (len(members) - 1) * step_for(k) <= span + 2 * gap), 3)
    positions = spread([c[along] for c in centres], step_for(lanes))
    best = None
    for shift in range(5):
        trial = dict(ctx, placed=list(ctx["placed"]), leaders=list(ctx["leaders"]))
        done = set()
        for lane in range(lanes):
            level = lane0 + sign * (lane * pitch + shift * pitch / 2)
            for f, pos in zip(members[lane::lanes], positions[lane::lanes]):
                # Small slides along the lane let an outer leader pass between inner balloons.
                b = f["bbox"]
                # Arrow lands on the middle of the label's free end, never beside it.
                edge = (b[cross] - tip_gap) if sign < 0 else (b[cross + 2] + tip_gap)
                mid = (b[along] + b[along + 2]) / 2
                tip = fitz.Point(mid, edge) if along == 0 else fitz.Point(edge, mid)
                for nudge in (0, 0.25, -0.25, 0.5, -0.5, 0.75, -0.75):
                    p = pos + nudge * gap
                    center = fitz.Point(p, level) if along == 0 else fitz.Point(level, p)
                    cand = evaluate(center, fitz.Rect(b), trial, tip)
                    if cand:
                        commit(trial, f, cand)
                        done.add(id(f))
                        break
        if best is None or len(done) > len(best[1]):
            best = (trial, done)
        if len(done) == len(members):
            break
    ctx["placed"][:], ctx["leaders"][:] = best[0]["placed"], best[0]["leaders"]
    return [f for f in members if id(f) not in best[1]]

def view_blocks(features, page_height, style, view_order=()):
    """Split features into drawing views (feature "view" field), ordered top-left to
    bottom-right by each view's top-left corner, or by an explicit view_order."""
    blocks = {}
    for f in features:
        blocks.setdefault(f.get("view") or "", []).append(f)
    def corner(name):
        boxes = [f["bbox"] for f in blocks[name]]
        return min(b[0] for b in boxes), min(b[1] for b in boxes)
    explicit = [v for v in view_order if v in blocks]
    rest = sorted((v for v in blocks if v not in explicit), key=lambda v: corner(v)[1])
    tol = float(style["order_block_tolerance"]) * page_height
    rows = []
    for name in rest:
        if rows and corner(name)[1] - corner(rows[-1][0])[1] <= tol:
            rows[-1].append(name)
        else:
            rows.append([name])
    ordered = explicit + [v for row in rows for v in sorted(row, key=lambda v: corner(v)[0])]
    return [(v, blocks[v]) for v in ordered]

def reading_order(features, groups, style, page_height=None, view_order=()):
    """Rank features view by view; inside each view top-left to bottom-right
    (rows by centre y, then left to right). Numbering runs on across views.
    A dense group is one unit numbered consecutively along its own axis."""
    if page_height is None:
        page_height = max([f["bbox"][3] for f in features] + [1])
    rank = {}
    for _, members in view_blocks(features, page_height, style, view_order):
        ids = {id(f) for f in members}
        local = reading_order_block(members, [g for g in groups if id(g["members"][0]) in ids], style)
        for f in sorted(members, key=lambda f: local[id(f)]):
            rank[id(f)] = len(rank)
    return rank

def reading_order_block(features, groups, style):
    tol = float(style["order_row_tolerance"])
    units, grouped = [], set()
    for group in groups:
        along = 0 if group["axis"] == "x" else 1
        members = sorted(group["members"], key=lambda f: feature_center(f)[along])
        units.append((feature_center(members[0]), members))
        grouped.update(id(m) for m in members)
    units += [(feature_center(f), [f]) for f in features if id(f) not in grouped]
    units.sort(key=lambda u: u[0][1])
    rows = []
    for unit in units:
        if rows and unit[0][1] - rows[-1][0][0][1] <= tol:
            rows[-1].append(unit)
        else:
            rows.append([unit])
    rank = {}
    for row in rows:
        for _, members in sorted(row, key=lambda u: u[0][0]):
            for f in members:
                rank[id(f)] = len(rank)
    return rank

def layout(page, features, protected, style, keep_out=(), view_order=()):
    style = dict(DEFAULT, **style)
    ctx = {"style": style, "radius": float(style["radius"]), "bounds": page.rect,
           "protected": protected, "keep_out": list(keep_out), "placed": [], "leaders": [],
           "segments": SegmentIndex(drawing_segments(page)), "raster": page_raster(page)}
    groups = find_dense_groups(features, style)
    grouped = {id(m) for g in groups for m in g["members"]}
    leftovers = []
    for group in groups:
        leftovers += place_group(group, ctx)
    review = []
    for feature in [f for f in features if id(f) not in grouped] + leftovers:
        cand = place_greedy(feature, ctx)
        if cand is None:
            review.append({"feature": feature, "reason": "NO_NONOVERLAPPING_ARROW_PLACEMENT"})
        else:
            commit(ctx, feature, cand)
    rank = reading_order(features, groups, style, page.rect.height, view_order)
    placed = sorted(ctx["placed"], key=lambda row: rank[id(row["feature"])])
    review.sort(key=lambda row: rank[id(row["feature"])])
    return placed, review

def draw_arrow_head(page, start, end, style):
    blue = tuple(float(v) for v in style["color"])
    line_width = float(style["line_width"])
    points = arrow_points(start, end, style)
    if points is None:
        return
    tip, left, right, back = points
    # The line ends underneath the filled triangle; no visible gap or double tip.
    dx, dy = end.x - start.x, end.y - start.y
    length = math.hypot(dx, dy)
    overlap = 0.6
    line_end = fitz.Point(back.x + dx / length * overlap,
                          back.y + dy / length * overlap)
    page.draw_line(start, line_end, color=blue, width=line_width, overlay=True)
    shape = page.new_shape()
    shape.draw_polyline([tip, left, right, tip])
    shape.finish(color=blue, fill=blue, width=0.35, closePath=True)
    shape.commit(overlay=True)

def draw_wedge(page, center, end, style):
    blue = tuple(float(v) for v in style["color"])
    points = wedge_points(center, end, style)
    if points is None:
        return
    left, right, tip = points
    shape = page.new_shape()
    shape.draw_polyline([left, tip, right])
    shape.finish(color=blue, width=float(style["line_width"]), lineJoin=1, closePath=False)
    shape.commit(overlay=True)

def draw(page, placed, style, next_number):
    blue = tuple(float(v) for v in style["color"])
    r = float(style["radius"])
    for row in placed:
        row["number"] = next_number
        next_number += 1
        a, b = row["leader"]
        if style.get("leader_style", "wedge") == "wedge":
            draw_wedge(page, fitz.Point(row["center"]), fitz.Point(b), style)
        else:
            draw_arrow_head(page, fitz.Point(a), fitz.Point(b), style)
        c = fitz.Point(row["center"])
        page.draw_circle(c, r, color=blue, fill=(1, 1, 1), width=style["line_width"], overlay=True)
        value = str(row["number"])
        f = float(style["font_size"]) * (0.89 if len(value) > 2 else 1)
        width = fitz.get_text_length(value, fontname="helv", fontsize=f)
        page.insert_text((c.x - width / 2, c.y + f * 0.34), value,
                         fontname="helv", fontsize=f, color=blue, overlay=True)
    return next_number

def write_xlsx(rows, output):
    # Minimal standards-conforming XLSX, avoids heavy dependencies.
    header = ["No.", "Page", "View", "Characteristic", "Kind", "Source", "Confidence", "Status", "Review note"]
    data = [header] + [[str(r.get(k, "")) for k in
             ("number", "page", "view", "text", "kind", "source", "confidence", "status", "note")] for r in rows]
    xml_rows = []
    for ridx, cells in enumerate(data, 1):
        cols = []
        for cidx, value in enumerate(cells):
            col = ""
            n = cidx + 1
            while n:
                n, rem = divmod(n - 1, 26)
                col = chr(65 + rem) + col
            cols.append('<c r="%s%d" t="inlineStr"><is><t>%s</t></is></c>' %
                        (col, ridx, escape(value)))
        xml_rows.append('<row r="%d">%s</row>' % (ridx, "".join(cols)))
    sheets = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>' + "".join(xml_rows) + '</sheetData></worksheet>'
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>')
        z.writestr("_rels/.rels", '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>')
        z.writestr("xl/workbook.xml", '<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Inspection" sheetId="1" r:id="rId1"/></sheets></workbook>')
        z.writestr("xl/_rels/workbook.xml.rels", '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>')
        z.writestr("xl/worksheets/sheet1.xml", sheets)

def reference_blue(doc):
    """Derive balloon ink colour from blue pixels in the supplied reference."""
    from statistics import median
    page = doc[0]
    pix = page.get_pixmap(matrix=fitz.Matrix(1, 1), colorspace=fitz.csRGB, alpha=False)
    arr = pix.samples
    sample = []
    for y in range(0, pix.height, 2):
        for x in range(0, pix.width, 2):
            offset = y * pix.stride + 3 * x
            r, g, b = arr[offset:offset + 3]
            if b > r + 27 and b > g + 12 and r < 160 and g < 195 and 100 < b < 230:
                sample.append((r, g, b))
    if len(sample) < 25:
        return None
    return [round(median(c[i] for c in sample) / 255, 3) for i in range(3)]

def process(args):
    source = Path(args.input).resolve()
    target_dir = Path(args.output_dir).resolve()
    doc = ensure_pdf(source)
    style = dict(DEFAULT)
    user_style = json.loads(Path(args.style).read_text(encoding="utf-8")) if args.style else {}
    style.update(user_style)
    if args.reference:
        reference = ensure_pdf(Path(args.reference))
        matched = reference_blue(reference)
        # An explicit style colour wins; the reference only fills in a missing one.
        if matched and "color" not in user_style:
            style["color"] = matched
        reference.close()
    external = features_from_json(Path(args.features), doc.page_count) if args.features else {}
    keep_out = keep_out_from_json(Path(args.features), doc.page_count) if args.features else {}
    view_order = []
    if args.features:
        raw = json.loads(Path(args.features).read_text(encoding="utf-8"))
        view_order = [str(v) for v in raw.get("view_order", [])] if isinstance(raw, dict) else []
    if source.parent == target_dir: 
        raise ValueError("Output directory must differ from source directory to preserve originals")
    target_dir.mkdir(parents=True, exist_ok=True)
    manifest, next_number, review_count, source_counts = [], 1, 0, []
    for page_ix, page in enumerate(doc):
        native, protected = candidate_text(page, style)
        supplied = external.get(page_ix, [])
        ocr_note = "NOT_NEEDED"
        if not native and not supplied and getattr(args, "ocr", "auto") == "auto":
            sys.path.insert(0, str(Path(__file__).resolve().parent))
            from ocr_adapter import detect
            supplied, ocr_note = detect(page, page_ix + 1, target_dir, classify, style)
        features = merge_features(native, supplied)
        if not features:
            preview = target_dir / ("page_%d_NEEDS_AI_OCR.png" % (page_ix + 1))
            zoom = min(2, 2400 / max(page.rect.width, page.rect.height))
            page.get_pixmap(matrix=fitz.Matrix(zoom, zoom), alpha=False).save(str(preview))
            manifest.append({"page": page_ix + 1, "status": "REQUIRES_AI_OCR",
                             "preview": preview.name, "native_count": 0,
                             "note": ocr_note})
            review_count += 1
            continue
        # Protect human- or AI-detected features as well as native text.
        protected += [fitz.Rect(f["bbox"]) for f in features]
        placed, review = layout(page, features, protected, style, keep_out.get(page_ix, []), view_order)
        next_number = draw(page, placed, style, next_number)
        for row in placed:
            f = row["feature"]
            manifest.append({"number": row["number"], "page": page_ix + 1,
                 "view": f.get("view", ""), "text": f["text"], "kind": f["kind"], "source": f["source"],
                 "confidence": f.get("confidence"), "status": "placed_unverified",
                 "bbox": f["bbox"], "bubble_center": row["center"], "leader": row["leader"]})
        for row in review:
            f = row["feature"]
            manifest.append({"number": "", "page": page_ix + 1, "view": f.get("view", ""), "text": f["text"],
                 "kind": f["kind"], "source": f["source"], "confidence": f.get("confidence"),
                 "status": "NEEDS_MANUAL_PLACEMENT", "note": row["reason"], "bbox": f["bbox"]})
        review_count += len(review)
        source_counts.append({"page": page_ix + 1, "pdf_text": len(native),
                              "ai_ocr": len(supplied), "placed": len(placed),
                              "manual_placement": len(review)})
    stem = source.stem
    outpdf = target_dir / (stem + "_ballooned.pdf")
    doc.save(str(outpdf), garbage=4, deflate=True)
    doc.close()
    csv_path = target_dir / (stem + "_inspection.csv")
    report_rows = manifest
    with csv_path.open("w", encoding="utf-8-sig", newline="") as h:
        keys = ["number", "page", "view", "text", "kind", "source", "confidence", "status", "note"]
        writer = csv.DictWriter(h, fieldnames=keys, extrasaction="ignore")
        writer.writeheader(); writer.writerows(report_rows)
    xlsx = target_dir / (stem + "_inspection.xlsx")
    write_xlsx(report_rows, xlsx)
    summary = {"source": str(source), "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
       "output_pdf": str(outpdf), "csv": str(csv_path), "xlsx": str(xlsx),
       "total_detected": len(report_rows), "ballooned": sum(bool(r.get("number")) for r in report_rows),
       "needs_review": review_count, "pages": source_counts, "annotations": manifest,
       "review_required": True, "note": "Auto-detection is not proof of completeness. Inspect every page and verify all symbols, tolerances and missed dimensions."}
    (target_dir / (stem + "_review.json")).write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({k: summary[k] for k in ["output_pdf","xlsx","total_detected","ballooned","needs_review","review_required"]}, ensure_ascii=False))
    return 0 if review_count == 0 and summary["ballooned"] > 0 else 3

def main():
    ap = argparse.ArgumentParser(description="Automatic inspection balloons from engineering PDF + AI/OCR feature JSON.")
    ap.add_argument("--input", required=True, help="Original valid PDF (never overwritten)")
    ap.add_argument("--output-dir", required=True, help="New output folder")
    ap.add_argument("--features", help="Optional AI/OCR detections JSON in PDF point coordinates")
    ap.add_argument("--reference", help="Validate an optional balloon style reference PDF")
    ap.add_argument("--ocr", choices=["auto", "off"], default="auto", help="Try local RapidOCR if no CAD text layer")
    ap.add_argument("--style", help="Optional JSON for radius, color, font size, etc")
    args = ap.parse_args()
    try: return process(args)
    except Exception as e:
        print(json.dumps({"error": type(e).__name__, "detail": str(e)}, ensure_ascii=False), file=sys.stderr)
        return 2

if __name__ == "__main__":
    raise SystemExit(main())
