import unittest
import numpy as np
from recognize import deduplicate, drawing_regions, ink_obstacles, rect, merge_symbol_runs, normalize_symbols, rotated_view, note_regions, excluded_run, dimension_runs


class RecognitionTests(unittest.TestCase):
    def test_only_numeric_callouts_returned_to_ocr_overlay(self):
        texts=["A-A","A","j","→","Ø","R","Rz40","(Ø9.8)","4-R3","45°"]
        runs=[{"text":text,"box":rect(i*100,0,20,20),"upright":True,"confidence":.99} for i,text in enumerate(texts)]
        self.assertEqual([r["text"] for r in dimension_runs(runs,[])],["Rz40","(Ø9.8)","4-R3","45°"])

    def test_full_width_portrait_title_table_excluded_from_first_row(self):
        image=np.full((1200,840),255,np.uint8)
        image[25:1170,80:82]=0
        image[25:1170,810:812]=0
        for y in (870,895,920,990,1070,1150):
            image[y:y+2,80:812]=0
        # Internal columns have a different left edge from the first rows.
        image[920:1152,270:272]=0
        for y in (945,970): image[y:y+2,270:710]=0
        _,title=drawing_regions(image)
        self.assertIsNotNone(title)
        self.assertEqual(title[:2],(80,870))

    def test_part_edges_without_table_grid_are_not_excluded(self):
        image=np.full((1200,840),255,np.uint8)
        image[830:900,80:82]=0
        image[830:832,80:700]=0
        image[890:892,80:700]=0
        self.assertIsNone(drawing_regions(image)[1])

    def test_note_continuation_digits_excluded_but_finish_kept(self):
        runs=[{"text":"Missing dimensions see 3D","box":rect(230,790,240,24)},
              {"text":"Size","box":rect(230,816,40,20)},
              {"text":"8015","box":rect(330,816,45,20)},
              {"text":"Rz40","box":rect(145,790,45,24)},
              {"text":"185","box":rect(350,590,30,20)}]
        regions=note_regions(runs)
        self.assertEqual([r["text"] for r in runs if not excluded_run(r["box"],regions)],["Rz40","185"])

    def test_malformed_diagonal_does_not_replace_complete_dimension(self):
        base={"confidence":.95,"upright":True,"box":rect(10,10,20,50)}
        result=deduplicate([{**base,"text":"(18'6"},{**base,"text":"9.8"}])
        self.assertEqual(result[0]["text"],"9.8")

    def test_explicit_engineering_symbols_are_joined_without_guessing(self):
        boxes=[([[0,0],[9,0],[9,10],[0,10]],"Φ",.9),([[10,0],[35,0],[35,10],[10,10]],"9,8",.98)]
        merged=merge_symbol_runs(boxes)
        self.assertEqual(len(merged),1)
        self.assertEqual(normalize_symbols(merged[0][1]),"Ø9.8")
        self.assertEqual(normalize_symbols("9.8"),"9.8")
        self.assertEqual(normalize_symbols("(φ7,8)"),"(Ø7.8)")

    def test_rotation_maps_detected_coordinates_back_to_source(self):
        import cv2
        img=np.full((80,120,3),255,np.uint8)
        for angle in (0,45,90,135):
            rotated,inverse=rotated_view(img,angle)
            forward=cv2.invertAffineTransform(inverse)
            point=np.array([[[35.,42.]]])
            transformed=cv2.transform(point,forward)
            restored=cv2.transform(transformed,inverse)
            self.assertTrue(np.allclose(restored,point))

    def test_full_vertical_callout_wins_over_partial_digits(self):
        full = {"text": "124.5", "confidence": .95, "upright": True, "box": rect(10, 10, 10, 60)}
        digit = {"text": "5", "confidence": .99, "upright": True, "box": rect(10, 50, 10, 15)}
        distinct = {"text": "124.5", "confidence": .95, "upright": True, "box": rect(90, 10, 10, 60)}
        result = deduplicate([digit, full, distinct])
        self.assertEqual(len(result), 2)
        self.assertEqual(result[0]["text"], "124.5")
        self.assertIn("5", result[0]["alternatives"])

    def test_ink_mask_keeps_single_pixel_lines(self):
        image = np.full((90, 120), 255, np.uint8)
        image[45, 12:105] = 0
        boxes = ink_obstacles(image)
        self.assertTrue(boxes)
        for x in (12, 50, 104):
            self.assertTrue(any(b["x"] <= x/120 <= b["x"]+b["w"] and b["y"] <= .5 <= b["y"]+b["h"] for b in boxes))

    def test_title_table_detected_without_using_specific_document_coordinates(self):
        image = np.full((900, 1200), 255, np.uint8)
        image[80:82, 20:1180] = 0
        image[700:702, 650:1180] = 0
        image[750:752, 650:1180] = 0
        image[800:802, 650:1180] = 0
        image[700:890, 650:652] = 0
        top, title = drawing_regions(image)
        self.assertGreater(top, 80)
        self.assertIsNotNone(title)
        self.assertEqual(title[:2], (650, 700))


if __name__ == "__main__":
    unittest.main()
