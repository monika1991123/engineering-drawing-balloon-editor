import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { balloonPDF, readEditState, BALLOON_STYLE, leaderSides } from './balloon-pdf.mjs';
import * as pdfjs from './vendor/pdf.mjs';

const require = createRequire(import.meta.url);
globalThis.PDFLib = require('./vendor/pdf-lib.min.js');
const { PDFDocument, degrees, rgb } = globalThis.PDFLib;

test('hollow tail joins inside the circle without a separate endpoint dot', () => {
  const circle = { c: { x: 100, y: 100 }, r: BALLOON_STYLE.radius };
  const sides = leaderSides({ a: { x: 40, y: 100 } }, circle);
  assert.equal(Math.abs(sides[1].y - sides[0].y), 3);
  for (const p of sides) assert.ok(Math.hypot(p.x - 100, p.y - 100) < circle.r);
});

test('tail width scales with the selected circle size', () => {
  for (const r of [6,9,12,15]) {
    const sides=leaderSides({a:{x:40,y:100}},{c:{x:100,y:100},r});
    assert.ok(Math.abs(Math.abs(sides[1].y-sides[0].y)-3*r/9)<1e-10);
  }
});

test('PDF export includes conflicts and skips candidates without positions', async () => {
  const doc = await PDFDocument.create(); doc.addPage();
  const bytes = await doc.save();
  const source=await pdfjs.getDocument({data:bytes.slice()}).promise;
  const vp=(await source.getPage(1)).getViewport({scale:1});
  const items=[{number:1,page:0,pending:true},{number:2,page:0,clean:false,balloon:{c:{x:100,y:80},r:9},leader:{a:{x:60,y:120}}}];
  const exported=await balloonPDF(bytes,{pages:[{index:0,strict:true}],items},[vp]);
  const result=await pdfjs.getDocument({data:exported}).promise;
  assert.deepEqual((await (await result.getPage(1)).getTextContent()).items.map(i=>i.str),['2']);
  await source.destroy();await result.destroy();
});

test('PDF export retains original vectors, all pages, and positions on cropped/rotated pages', async () => {
  const original = await PDFDocument.create();
  for (const angle of [0, 90, 180, 270]) {
    const page = original.addPage([400, 300]);
    page.setCropBox(20, 30, 350, 250);
    page.setRotation(degrees(angle));
    page.drawLine({ start: { x: 50, y: 70 }, end: { x: 200, y: 200 }, color: rgb(0, 0, 0) });
  }
  const bytes = await original.save();
  const snapshot = bytes.slice();
  const source = await pdfjs.getDocument({ data: bytes.slice() }).promise;
  const viewports = [];
  const items = [];
  for (let i = 0; i < source.numPages; i++) {
    viewports.push((await source.getPage(i + 1)).getViewport({ scale: 1 }));
    items.push({ page: i, number: i + 1, balloon: { c: { x: 100, y: 80 }, r: 12 },
      leader: { a: { x: 60, y: 120 }, b: { x: 92, y: 88 } } });
  }
  const exported = await balloonPDF(bytes, { items }, viewports);
  assert.deepEqual(bytes, snapshot, 'source file bytes must stay intact');
  const result = await pdfjs.getDocument({ data: exported }).promise;
  assert.equal(result.numPages, 4);
  for (let i = 0; i < 4; i++) {
    const before = await source.getPage(i + 1);
    const after = await result.getPage(i + 1);
    assert.deepEqual(after.view, before.view);
    assert.equal(after.rotate, before.rotate);
    const text = await after.getTextContent();
    assert.deepEqual(text.items.map((x) => x.str), [String(i + 1)]);
    const tx = pdfjs.Util.transform(viewports[i].transform, text.items[0].transform);
    assert.ok(Math.abs(tx[5] - (80 + 12 * BALLOON_STYLE.fontRatio * 0.36)) < 0.01, 'label baseline follows viewport coordinates');
    assert.ok(Math.abs(tx[1]) < 0.01 && tx[0] > 0 && tx[3] < 0, 'label is upright on rotated pages');
    const oldOps = await before.getOperatorList();
    const newOps = await after.getOperatorList();
    const paths = (ops) => ops.fnArray.filter((op) => op === pdfjs.OPS.constructPath).length;
    assert.equal(paths(newOps) - paths(oldOps), 3, 'two tail edges and one circle, with no endpoint dot');
  }
  await source.destroy();
  await result.destroy();
});

test('exported PDF reopens on its clean source with editable state', async () => {
  const original = await PDFDocument.create(); original.addPage([400, 300]);
  const bytes = await original.save();
  const source = await pdfjs.getDocument({ data: bytes.slice() }).promise;
  const vp = (await source.getPage(1)).getViewport({ scale: 1 });
  const drawing = { pages: [{ index: 0, width: 400, height: 300 }],
    items: [{ id: 'a', number: 1, page: 0, balloon: { c: { x: 100, y: 80 }, r: 9 }, leader: { a: { x: 60, y: 120 } } },
      { id: 'b', number: 2, page: 0, pending: true }] };
  const exported = await balloonPDF(bytes, drawing, [vp], { filename: 'part.pdf', drawing });
  const reopened = await pdfjs.getDocument({ data: exported.slice() }).promise;
  const saved = await readEditState(reopened);
  assert.deepEqual(saved.source, bytes, 'embedded source is the unballooned original');
  assert.equal(saved.state.filename, 'part.pdf');
  assert.deepEqual(saved.state.drawing, drawing, 'pending items survive too');
  const clean = await pdfjs.getDocument({ data: saved.source.slice() }).promise;
  assert.deepEqual((await (await clean.getPage(1)).getTextContent()).items, [], 'no flattened balloons in the source');
  assert.equal(await readEditState(source), null, 'a plain PDF has no edit state');
  await Promise.all([source.destroy(), reopened.destroy(), clean.destroy()]);
});
