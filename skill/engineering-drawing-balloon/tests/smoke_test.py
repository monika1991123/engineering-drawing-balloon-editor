#!/usr/bin/env python3
"""Smoke test in a private, unique temp directory; no writes to shared fixtures."""
import sys
import json
import tempfile
import importlib.util
from pathlib import Path
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[1]
import pymupdf as fitz

spec = importlib.util.spec_from_file_location("balloon", ROOT / "scripts" / "balloon.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

# Distinct directory for every invocation keeps runs independent.
out = Path(tempfile.mkdtemp(prefix="engineering-balloon-smoke-"))
src = out / "demo.pdf"
doc = fitz.open()
page = doc.new_page(width=842, height=595)
page.draw_rect(fitz.Rect(190, 175, 475, 365), color=(0, 0, 0), width=1)
for text, x, y in [
    ("124,5", 145, 265), ("109,5", 300, 155), ("41", 340, 390),
    ("Rz40", 560, 310), ("2", 240, 430), ("Drawing", 590, 530)
]:
    page.insert_text((x, y), text, fontname="helv", fontsize=9)
doc.save(str(src))
doc.close()

result_dir = out / "result"
rc = m.process(SimpleNamespace(
    input=str(src), output_dir=str(result_dir),
    features=None, reference=None, style=None, ocr="off"
))
report = json.loads((result_dir / "demo_review.json").read_text(encoding="utf-8"))
assert rc == 0, "Balloon engine exit code: %s" % rc
assert report["ballooned"] >= 3, report
with m.ensure_pdf(result_dir / "demo_ballooned.pdf") as result:
    assert result.page_count == 1
assert (result_dir / "demo_inspection.xlsx").exists()
assert (result_dir / "demo_inspection.csv").exists()
print("PASS:", report["ballooned"], "balloons; needs_review:", report["needs_review"])
print("Isolated test location:", out)
