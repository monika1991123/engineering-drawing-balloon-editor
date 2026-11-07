"""Offline, CPU-only OCR. One PNG on stdin; normalized JSON on stdout.

Models ship inside the pinned RapidOCR wheel. No network requests or image
files are used during recognition. Coordinates are fractions of the image.
"""
import json
import math
import os
import re
import sys

os.environ.setdefault("OMP_NUM_THREADS", "1")
os.environ.setdefault("OPENBLAS_NUM_THREADS", "1")

import cv2
import numpy as np
import onnxruntime as ort
from rapidocr_onnxruntime import RapidOCR

ort.disable_telemetry_events()


def rect(x, y, w, h):
    return {"x": float(x), "y": float(y), "w": float(w), "h": float(h)}


def normalize_symbols(text):
    text = text.translate(str.maketrans({"⌀":"Ø", "Φ":"Ø", "φ":"Ø", "ϕ":"Ø", "ø":"Ø", "∅":"Ø", "º":"°", "˚":"°"}))
    return re.sub(r"(?<=\d),(?=\d)", ".", text.strip())


def rotated_view(image, angle):
    h,w=image.shape[:2]
    if angle==0:
        return image, np.array([[1.,0,0],[0,1.,0]])
    if angle==90:
        return np.rot90(image).copy(),np.array([[0.,-1,w-1],[1.,0,0]])
    matrix=cv2.getRotationMatrix2D(((w-1)/2,(h-1)/2),angle,1)
    nw=math.ceil(h*abs(matrix[0,1])+w*abs(matrix[0,0]))
    nh=math.ceil(h*abs(matrix[0,0])+w*abs(matrix[0,1]))
    matrix[0,2]+=(nw-w)/2;matrix[1,2]+=(nh-h)/2
    return cv2.warpAffine(image,matrix,(nw,nh),borderValue=(255,255,255)),cv2.invertAffineTransform(matrix)


def merge_symbol_runs(result):
    """Join explicitly detected symbols to their adjacent values, never infer
    a missing diameter/degree from an ambiguous letter or plain number."""
    rows=list(result or [])
    changed=True
    while changed:
        changed=False
        for i,(points,text,confidence) in enumerate(rows):
            a=np.asarray(points);ax,ay=a.min(axis=0);ar,ab=a.max(axis=0)
            for j,(other,other_text,other_confidence) in enumerate(rows):
                if i==j:continue
                if text.strip() not in {"Ø","Φ","φ","ϕ","⌀","∅","R","("} and other_text.strip() not in {"°","º","˚",")"} and not re.fullmatch(r"\d+\s*-",text.strip()):continue
                b=np.asarray(other);bx,by=b.min(axis=0);br,bb=b.max(axis=0)
                height=max(ab-ay,bb-by)
                if height<=0 or not (-.15*height<=bx-ar<=.65*height):continue
                if abs((ay+ab-by-bb)/2)>.45*height:continue
                combined=text.strip()+other_text.strip()
                if not re.search(r"\d",combined) and combined not in {"(Ø","(Φ","(⌀","(R"}:continue
                x,y=min(ax,bx),min(ay,by);right,bottom=max(ar,br),max(ab,bb)
                rows[i]=([[float(x),float(y)],[float(right),float(y)],[float(right),float(bottom)],[float(x),float(bottom)]],combined,min(confidence,other_confidence))
                rows.pop(j);changed=True;break
            if changed:break
    return rows


def intersection(a, b):
    return max(0, min(a["x"] + a["w"], b["x"] + b["w"]) - max(a["x"], b["x"])) * max(
        0, min(a["y"] + a["h"], b["y"] + b["h"]) - max(a["y"], b["y"]))


def deduplicate(runs):
    # Prefer a full upright string over partial detections of rotated digits.
    # A longer malformed diagonal reading must not replace a complete number.
    complete = re.compile(r"\(?\s*(?:\d+\s*[-Xx×]\s*)?(?:Ø|SR|R)?\s*\d+(?:\.\d+)?\s*°?\s*\)?")
    ranked = sorted(runs, key=lambda r: (bool(complete.fullmatch(r["text"])), r["upright"], len(r["text"]), r["confidence"]), reverse=True)
    kept = []
    for run in ranked:
        duplicate = False
        for old in kept:
            area = min(run["box"]["w"] * run["box"]["h"], old["box"]["w"] * old["box"]["h"])
            if area and intersection(run["box"], old["box"]) / area > .45:
                if run["text"] != old["text"] and run["upright"] == old["upright"]:
                    old["alternatives"] = sorted(set(old.get("alternatives", []) + [run["text"]]))
                duplicate = True
                break
        if not duplicate:
            kept.append(run)
    return sorted(kept, key=lambda r: (round(r["box"]["y"] / 15), r["box"]["x"]))


def drawing_regions(gray):
    h, w = gray.shape
    binary = (gray < 190).astype(np.uint8)
    lines = cv2.morphologyEx(binary, cv2.MORPH_OPEN, np.ones((1, max(51, w // 5) | 1), np.uint8))
    _, _, stats, _ = cv2.connectedComponentsWithStats(lines)
    vertical = cv2.morphologyEx(binary, cv2.MORPH_OPEN, np.ones((max(31, h // 16) | 1, 1), np.uint8))
    _, _, vstats, _ = cv2.connectedComponentsWithStats(vertical)
    title = None
    top = 0
    for x, y, rw, rh, _ in stats[1:]:
        if rw > .75 * w and y < .22 * h:
            top = max(top, int(y + rh + 8))
        if x > .045 * w and rw > .27 * w and y > .55 * h:
            # A long part edge is not a title table: require a left vertical
            # boundary and multiple table rows below this candidate top edge.
            boundary = any(abs(int(vx) - int(x)) < 8 and vy <= y + 8 and vy + vh > y + .08*h
                           for vx, vy, vw, vh, area in vstats[1:])
            row_count = sum(abs(int(rx) - int(x)) < 10 and ry >= y and rrw > .8*rw for rx, ry, rrw, rrh, area in stats[1:])
            if boundary and row_count >= 3 and (title is None or y < title[1]):
                title = (int(x), int(y), int(w - x), int(h - y))
    return top, title


def note_regions(runs):
    """Exclude explanatory text above a title table, including split OCR digits
    on its continuation line. Do not remove adjacent finish callouts such as Rz40.
    Coordinates here are still source-image pixels, before normalization.
    """
    regions = []
    for run in runs:
        text = run["text"]
        b = run["box"]
        missing = re.search(r"missing\s+dimensions?", text, re.I)
        standard = re.search(r"^\s*size\s+(?:ISO|IS0)\b", text, re.I)
        if not (missing or standard):
            continue
        pad = b["h"] * .2
        regions.append(rect(b["x"]-pad, b["y"]-pad, b["w"]+2*pad,
                            b["h"]*(2.6 if missing else 1.4)))
    return regions


def excluded_run(box, regions):
    # Use the center, so a diagonal OCR box slightly crossing a boundary cannot
    # leak title text back into candidates after the initial image mask.
    x, y = box["x"]+box["w"]/2, box["y"]+box["h"]/2
    return any(b["x"] <= x <= b["x"]+b["w"] and b["y"] <= y <= b["y"]+b["h"] for b in regions)


def dimension_runs(runs, excluded):
    # Keep nonnumeric runs until note filtering and symbol merging have used
    # their context. Return only numeric callouts to the editor's OCR boxes.
    return deduplicate([r for r in runs if re.search(r"[0-9]", r["text"])
                        and not excluded_run(r["box"], excluded)])


def ink_obstacles(gray, step=9):
    """Conservative occupied cells; row spans compress thin drawing geometry."""
    h, w = gray.shape
    rows, cols = math.ceil(h / step), math.ceil(w / step)
    padded = np.full((rows * step, cols * step), 255, dtype=np.uint8)
    padded[:h, :w] = gray
    cells = padded.reshape(rows, step, cols, step).min(axis=(1, 3)) < 205
    out = []
    for y, row in enumerate(cells):
        edges = np.flatnonzero(np.diff(np.r_[False, row, False].astype(np.int8)))
        for left, right in zip(edges[::2], edges[1::2]):
            out.append(rect(left * step / w, y * step / h, (right - left) * step / w, step / h))
    return out


def recognize(image):
    h, w = image.shape[:2]
    gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
    top, title = drawing_regions(gray)
    work = image.copy()
    if top:
        work[:top] = 255
    # Suppress drawing-frame labels and title table for recognition only. Their
    # ink still participates in collision avoidance.
    work[:, :int(w * .045)] = 255
    work[:, int(w * .985):] = 255
    if title:
        x, y, tw, th = title
        work[y:y + th, x:x + tw] = 255
    engine = RapidOCR(intra_op_num_threads=1, inter_op_num_threads=1,
                      det_limit_side_len=1400, det_limit_type="max", max_side_len=1600,
                      text_score=.55, det_box_thresh=.4)
    runs = []
    # Include diagonal leaders such as 4-R3 and 4-Ø4 as well as vertical text.
    for angle in (0, 90, 45, 135):
        view,inverse = rotated_view(work,angle)
        vh, vw = view.shape[:2]
        tile, overlap = 1400, 160
        starts = lambda size: sorted(set(range(0, max(1, size - tile + 1), tile - overlap)) | {max(0, size - tile)})
        for oy in starts(vh):
            for ox in starts(vw):
                crop = view[oy:oy + tile, ox:ox + tile]
                if np.count_nonzero(crop < 200) < 30:
                    continue
                result, _ = engine(crop, use_cls=angle != 0)
                for points, text, confidence in merge_symbol_runs(result):
                    pts = np.asarray(points, dtype=float) + [ox, oy]
                    width = np.linalg.norm(pts[1] - pts[0])
                    height = np.linalg.norm(pts[3] - pts[0])
                    # OCR internally rotates tall crops too; prefer the view
                    # where a numeric dimension was actually detected upright.
                    upright = width >= height * .8
                    pts=cv2.transform(np.expand_dims(pts,0),inverse)[0]
                    x1, y1 = pts.min(axis=0)
                    x2, y2 = pts.max(axis=0)
                    x1,y1=max(0,x1),max(0,y1)
                    x2,y2=min(w,x2),min(h,y2)
                    if x2<=x1 or y2<=y1:continue
                    if title and x1 >= title[0] and y1 >= title[1]:
                        continue
                    raw = text.strip()
                    text = normalize_symbols(raw)
                    runs.append({"text": text, "raw_text": raw, "box": rect(x1, y1, x2-x1, y2-y1),
                                 "confidence": float(confidence), "rotation": angle, "upright": upright})
    excluded = note_regions(runs)
    if title:
        excluded.append(rect(*title))
    runs = dimension_runs(runs, excluded)
    for run in runs:
        b = run["box"]
        run["box"] = rect(b["x"] / w, b["y"] / h, b["w"] / w, b["h"] / h)
        del run["upright"]
    obstacles = ink_obstacles(gray)
    if title:
        # Empty title cells are outside the drawing as well: do not let the
        # placement solver use them as apparent whitespace for nearby balloons.
        obstacles.append(rect(title[0]/w, title[1]/h, title[2]/w, title[3]/h))
    return {"texts": runs, "obstacles": obstacles,
            "excluded_title_block": rect(title[0]/w, title[1]/h, title[2]/w, title[3]/h) if title else None,
            "engine": "RapidOCR PP-OCRv4 ONNX CPU (offline)"}


if __name__ == "__main__":
    payload = sys.stdin.buffer.read(16 * 1024 * 1024 + 1)
    if len(payload) > 16 * 1024 * 1024:
        raise ValueError("PNG exceeds 16 MB")
    image = cv2.imdecode(np.frombuffer(payload, np.uint8), cv2.IMREAD_COLOR)
    if image is None or image.shape[0] * image.shape[1] > 16_000_000:
        raise ValueError("Invalid PNG or more than 16 million pixels")
    json.dump(recognize(image), sys.stdout, ensure_ascii=False)
