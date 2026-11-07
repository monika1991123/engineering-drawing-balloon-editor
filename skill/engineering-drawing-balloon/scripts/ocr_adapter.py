"""Optional lightweight offline OCR adapter (RapidOCR + ONNX runtime).
Does not install dependencies or download models.
"""
from pathlib import Path

def detect(page, page_number, output_dir, classify, style):
    try:
        from rapidocr import RapidOCR
    except ImportError:
        return [], "LOCAL_OCR_NOT_INSTALLED"
    try:
        import pymupdf as fitz
        zoom = min(2.5, 2500 / max(page.rect.width, page.rect.height))
        path = Path(output_dir) / ("page_%d_ocr.png" % page_number)
        page.get_pixmap(matrix=fitz.Matrix(zoom, zoom), alpha=False).save(str(path))
        result = RapidOCR()(str(path))
        if result is None or result.txts is None or result.boxes is None:
            return [], "LOCAL_OCR_EMPTY"
        detections = []
        for txt, box, prob in zip(result.txts, result.boxes, result.scores):
            kind = classify(str(txt))
            if not kind or float(prob) < 0.60:
                continue
            points = [(float(pt[0]) / zoom, float(pt[1]) / zoom) for pt in box]
            x0, y0 = min(x for x, y in points), min(y for x, y in points)
            x1, y1 = max(x for x, y in points), max(y for x, y in points)
            cx, cy = (x0+x1)/2, (y0+y1)/2
            if cy < page.rect.height * style["top_grid_ignore"] or cx < page.rect.width * style["left_grid_ignore"]:
                continue
            if cy > page.rect.height * style["title_block_bottom"] and cx > page.rect.width * style["title_block_right"]:
                continue
            detections.append({"text": str(txt), "bbox": [x0,y0,x1,y1], "kind": kind,
                               "confidence": float(prob), "source": "rapidocr"})
        return detections, "LOCAL_OCR_COMPLETED"
    except Exception as exc:
        return [], "LOCAL_OCR_ERROR:" + type(exc).__name__
