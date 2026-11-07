// balloon — browser side.
//
// The original PDF stays in the browser. Offline OCR sends rendered page PNGs
// only to this server, never to an external model service.

import * as pdfjsLib from '/vendor/pdf.mjs';
import { balloonPDF, readEditState, BALLOON_STYLE, leaderSides } from '/balloon-pdf.mjs';

pdfjsLib.GlobalWorkerOptions.workerSrc = '/vendor/pdf.worker.mjs';

const SVG_NS = 'http://www.w3.org/2000/svg';
const $ = (id) => document.getElementById(id);

const state = {
  pdf: null,        // the loaded PDFDocumentProxy, null in demo mode
  drawing: null,    // the model.Drawing the server built
  texts: [],        // every extracted run, all pages
  page: 0,          // zero-based page index
  zoom: 1.3,
  selected: null,   // item id
  bytes: null,     // original PDF, kept separately from PDF.js's worker buffer
  filename: '',
  adding: false,
  noText: false,
  busy: false,
  ocrTexts: [],
  showOcr: false,
  revision: 0,
  validating: false,
  validationToken: 0,
  ocrDone: new Set(),
  ocrError: '',
  showPending: false,
  showConflicts: false,
  exporting: false,
};

// ---------------------------------------------------------------- extraction

// PDF text layers split a callout across runs almost every time: "⌀12.50" and
// " ±0.05" commonly arrive as separate items, and a parser handed the halves
// separately produces two useless characteristics instead of one correct one.
// Runs sitting on the same baseline with only a small gap between them are
// therefore stitched back together before anything else looks at them.
function mergeRuns(items) {
  const sorted = [...items].sort((a, b) =>
    Math.abs(a.box.y - b.box.y) > 2 ? a.box.y - b.box.y : a.box.x - b.box.x);

  const out = [];
  for (const it of sorted) {
    const prev = out[out.length - 1];
    if (prev && sameLine(prev, it) && gap(prev, it) < Math.max(prev.box.h, 4) * 1.2) {
      const right = Math.max(prev.box.x + prev.box.w, it.box.x + it.box.w);
      // Re-insert a space only when the glyphs were actually separated.
      const sep = gap(prev, it) > prev.box.h * 0.18 ? ' ' : '';
      prev.text += sep + it.text;
      prev.box.w = right - prev.box.x;
      prev.box.h = Math.max(prev.box.h, it.box.h);
      continue;
    }
    out.push({ text: it.text, page: it.page, box: { ...it.box } });
  }
  return out.filter((i) => i.text.trim() !== '');
}

const sameLine = (a, b) =>
  Math.abs((a.box.y + a.box.h / 2) - (b.box.y + b.box.h / 2)) < Math.max(a.box.h, b.box.h) * 0.55;

const gap = (a, b) => b.box.x - (a.box.x + a.box.w);

async function extractPage(page, index) {
  const vp = page.getViewport({ scale: 1 });
  const content = await page.getTextContent();

  const runs = [];
  for (const item of content.items) {
    if (!item.str || !item.str.trim()) continue;
    // Map the run's text-space transform into viewport space, which is already
    // top-left origin — the coordinate system the Go side works in.
    const tx = pdfjsLib.Util.transform(vp.transform, item.transform);
    const h = Math.hypot(tx[2], tx[3]) || 8;
    runs.push({
      text: item.str,
      page: index,
      box: { x: tx[4], y: tx[5] - h, w: item.width || 1, h },
    });
  }
  return { runs: mergeRuns(runs), width: vp.width, height: vp.height };
}

// ------------------------------------------------------------------- loading

async function loadPDF(file) {
  if (state.busy) return;
  toast(`Reading ${file.name}…`);
  const buf = await file.arrayBuffer();
  let bytes = new Uint8Array(buf.slice(0));
  let pdf = await pdfjsLib.getDocument({ data: buf }).promise;
  // A PDF exported by this editor reopens on its clean source with the
  // balloons restored as editable items rather than as flattened graphics.
  let saved = null;
  try { saved = await readEditState(pdf); }
  catch (err) { toast(`内嵌编辑数据无法读取，按普通 PDF 打开：${err.message}`, true); }
  if (saved) {
    await pdf.destroy();
    bytes = saved.source;
    pdf = await pdfjsLib.getDocument({ data: saved.source.slice() }).promise;
  }
  state.bytes = bytes;
  state.filename = saved?.state.filename || file.name;
  state.pdf = pdf;

  const pages = [];
  let texts = [];
  for (let n = 1; n <= state.pdf.numPages; n++) {
    const page = await state.pdf.getPage(n);
    const { runs, width, height } = await extractPage(page, n - 1);
    pages.push({ index: n - 1, width, height });
    texts = texts.concat(runs);
  }

  state.texts = texts;
  state.page = 0;
  state.selected = null;
  state.ocrTexts = [];
  state.ocrDone.clear(); state.ocrError = '';
  $('balloonSize').value = String(BALLOON_STYLE.radius);
  state.showPending = false;
  state.showConflicts = false;
  state.showOcr = false;
  state.revision++;
  state.validationToken++; state.validating = false;
  $('ocrStatus').hidden = true;
  $('selectionPreview').hidden = true;
  state.noText = texts.length === 0;
  state.zoom = Math.max(0.3, Math.min(1.3, ($('stage').clientWidth - 40) / pages[0].width));

  const guessed = file.name.replace(/\.pdf$/i, '');
  if (!$('partNumber').value) $('partNumber').value = guessed;

  const meta = {
    id: 'local',
    name: $('partName').value,
    part_number: $('partNumber').value || guessed,
    revision: $('revision').value,
    pages,
    balloon_radius: BALLOON_STYLE.radius,
  };
  if (saved && restoreEdit(saved.state, pages)) {
    setAdding(false);
    await renderPage();
    validatePositions();
    toast(`已恢复导出时的 ${state.drawing.items.length} 个标注，可继续编辑后再次导出。`);
    return;
  }
  // A vector-only drawing still supports a complete manual annotation flow.
  if (state.noText) state.drawing = { ...meta, items: [] };
  else await build(meta);

  setAdding(state.noText);
  await renderPage();
  toast(state.noText ? '此 PDF 没有文字层。点击尺寸位置添加气泡，再拖动圆圈调整位置。' : `${state.drawing.items.length} characteristics found`);
}

function restoreEdit(saved, pages) {
  const d = saved.drawing;
  const samePages = d.pages?.length === pages.length && pages.every((p, i) =>
    Math.abs(d.pages[i].width - p.width) < 0.5 && Math.abs(d.pages[i].height - p.height) < 0.5);
  if (!samePages) { toast('内嵌编辑数据与原图页面不一致，按普通 PDF 打开', true); return false; }
  state.drawing = d;
  state.ocrTexts = saved.ocr_texts || [];
  for (const index of saved.ocr_done || []) state.ocrDone.add(index);
  const meta = saved.meta || {};
  $('partNumber').value = meta.partNumber ?? d.part_number ?? '';
  $('partName').value = meta.partName ?? d.name ?? '';
  $('revision').value = meta.revision ?? d.revision ?? '';
  $('serial').value = meta.serial ?? '';
  const size = String(d.balloon_radius || BALLOON_STYLE.radius);
  if ([...$('balloonSize').options].some((o) => o.value === size)) $('balloonSize').value = size;
  return true;
}

async function loadDemo() {
  const res = await fetch('/api/demo');
  if (!res.ok) return toast('Could not load the demo', true);
  const { drawing, texts } = await res.json();

  state.pdf = null;
  state.bytes = null;
  state.noText = false;
  state.selected = null;
  state.ocrTexts = [];
  state.ocrDone.clear(); state.ocrError = '';
  $('balloonSize').value = String(drawing.balloon_radius || BALLOON_STYLE.radius);
  state.showPending = false;
  state.showConflicts = false;
  state.showOcr = false;
  state.validationToken++; state.validating = false;
  $('ocrStatus').hidden = true;
  $('selectionPreview').hidden = true;
  setAdding(false);
  state.texts = texts;
  state.page = 0;
  state.drawing = drawing;

  $('partNumber').value = drawing.part_number || '';
  $('partName').value = drawing.name || '';
  $('revision').value = drawing.revision || '';

  await renderPage();
  toast(`Demo part loaded — ${drawing.items.length} characteristics`);
}

async function build(meta) {
  const body = {
    drawing: meta,
    texts: state.texts,
    tolerances: {
      1: num($('tol1').value, 0.2),
      2: num($('tol2').value, 0.1),
      3: num($('tol3').value, 0.05),
    },
    angular: num($('tolAng').value, 0.5),
    unit: 'mm',
  };
  const res = await post('/api/build', body);
  if (res) state.drawing = { ...res, items: res.items || [] };
}

// ------------------------------------------------------------------ rendering

async function renderPage() {
  const d = state.drawing;
  if (!d) return;
  if (findItem(state.selected)?.page !== state.page) {
    state.selected = null; $('selectionPreview').hidden = true;
  }

  $('empty').hidden = true;
  $('sheet').hidden = false;

  const page = d.pages.find((p) => p.index === state.page) || d.pages[0];
  const canvas = $('canvas');
  const overlay = $('overlay');

  const cssW = page.width * state.zoom;
  const cssH = page.height * state.zoom;

  // The sheet is sized independently of the canvas so it still occupies the
  // page area in demo mode, where there is no PDF to render behind it.
  $('sheet').style.width = `${cssW}px`;
  $('sheet').style.height = `${cssH}px`;

  if (state.pdf) {
    const dpr = window.devicePixelRatio || 1;
    const pdfPage = await state.pdf.getPage(state.page + 1);
    const vp = pdfPage.getViewport({ scale: state.zoom * dpr });
    canvas.width = vp.width;
    canvas.height = vp.height;
    canvas.style.width = `${cssW}px`;
    canvas.style.height = `${cssH}px`;
    canvas.hidden = false;
    await pdfPage.render({ canvasContext: canvas.getContext('2d'), viewport: vp }).promise;
  } else {
    // Demo mode has no PDF behind it: hide the canvas so the sheet's own white
    // background shows through as paper, and let the overlay draw the callouts
    // alongside the balloons.
    canvas.hidden = true;
  }

  overlay.setAttribute('width', cssW);
  overlay.setAttribute('height', cssH);
  overlay.setAttribute('viewBox', `0 0 ${page.width} ${page.height}`);

  drawOverlay(page);
  drawTable();
  updateChrome(d);
}

function drawOverlay(page) {
  const overlay = $('overlay');
  overlay.textContent = '';

  if (!state.pdf) {
    for (const t of state.texts.filter((t) => t.page === state.page)) {
      const el = document.createElementNS(SVG_NS, 'text');
      el.setAttribute('x', t.box.x);
      el.setAttribute('y', t.box.y + t.box.h * 0.78);
      el.setAttribute('font-size', t.box.h * 0.85);
      el.setAttribute('fill', '#16181d');
      el.setAttribute('font-family', 'system-ui, sans-serif');
      el.textContent = t.text;
      overlay.appendChild(el);
    }
  }

  if (state.showOcr) {
    for (const text of state.ocrTexts.filter((t) => t.page === state.page)) {
      overlay.appendChild(boxNode(text.box, 'ocr-box'));
    }
  }
  for (const item of itemsOnPage()) {
    if (!item.pending) overlay.appendChild(balloonNode(item));
    if (item.id === state.selected) {
      const highlight = boxNode(item.source.box, 'source-highlight');
      if (item.pending) highlight.classList.add('pending-source');
      overlay.appendChild(highlight);
    }
  }
}

function boxNode(box, cls) {
  const rect = document.createElementNS(SVG_NS, 'rect');
  rect.setAttribute('x', box.x - 2); rect.setAttribute('y', box.y - 2);
  rect.setAttribute('width', box.w + 4); rect.setAttribute('height', box.h + 4);
  rect.classList.add(cls);
  return rect;
}

function balloonNode(item) {
  const g = document.createElementNS(SVG_NS, 'g');
  g.classList.add('balloon');
  if (!item.clean) g.classList.add('unclean');
  if (!item.include && !item.manual && !item.source.ocr) g.classList.add('excluded');
  if (item.id === state.selected) g.classList.add('selected');
  g.dataset.id = item.id;
  g.style.setProperty('--balloon-width', BALLOON_STYLE.lineWidth * item.balloon.r / BALLOON_STYLE.radius);

  const leader = document.createElementNS(SVG_NS, 'path');
  leader.classList.add('leader');
  const [left, right] = leaderSides(item.leader, item.balloon);
  leader.setAttribute('d', `M ${left.x} ${left.y} L ${item.leader.a.x} ${item.leader.a.y} L ${right.x} ${right.y}`);
  g.appendChild(leader);

  const tip = document.createElementNS(SVG_NS, 'circle');
  tip.classList.add('tip');
  tip.setAttribute('cx', item.leader.a.x);
  tip.setAttribute('cy', item.leader.a.y);
  tip.setAttribute('r', 2);
  g.appendChild(tip);

  const circle = document.createElementNS(SVG_NS, 'circle');
  circle.setAttribute('cx', item.balloon.c.x);
  circle.setAttribute('cy', item.balloon.c.y);
  circle.setAttribute('r', item.balloon.r);
  g.appendChild(circle);

  const label = document.createElementNS(SVG_NS, 'text');
  label.setAttribute('x', item.balloon.c.x);
  label.setAttribute('y', item.balloon.c.y + item.balloon.r * BALLOON_STYLE.fontRatio * 0.36);
  label.setAttribute('font-size', item.balloon.r * BALLOON_STYLE.fontRatio);
  label.textContent = item.number;
  g.appendChild(label);

  const title = document.createElementNS(SVG_NS, 'title');
  title.textContent = item.issues?.length
    ? `${item.requirement}\n⚠ ${item.issues.join('\n⚠ ')}`
    : item.requirement;
  g.appendChild(title);

  return g;
}

function drawTable() {
  const tbody = $('tbody');
  tbody.textContent = '';
  const items = itemsOnPage();

  if (!items.length) {
    tbody.innerHTML = '<tr class="placeholder"><td colspan="4">No characteristics on this sheet.</td></tr>';
    return;
  }

  for (const item of items) {
    const tr = document.createElement('tr');
    tr.dataset.id = item.id;
    tr.dataset.pending = String(!!item.pending);
    if (item.id === state.selected) tr.classList.add('selected');
    if (!item.include && !item.source.ocr) tr.classList.add('excluded');
    if (!item.clean || item.characteristic.warnings?.length) tr.classList.add('flagged');

    const num = cell('td', item.number, 'c num');

    const req = cell('td', item.requirement, 'req editable');
    req.contentEditable = 'true';
    req.spellcheck = false;
    req.title = `as extracted: ${item.source.text}`;
    req.addEventListener('blur', () => reparse(item.id, req.textContent.trim()));
    req.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); req.blur(); }
      if (e.key === 'Escape') { req.textContent = item.requirement; req.blur(); }
    });

    const lim = cell('td', item.limits || '—', 'lim');
    if (item.source.ocr) {
      const review = cell('span', `${item.pending ? '待放置 · ' : ''}${item.review_required ? '待核对' : '已核对'} · 得分 ${item.source.confidence.toFixed(2)}`, 'review-label');
      const confirm = cell('button', item.pending ? '手动放置' : item.review_required ? '确认识别' : '查看原图', 'btn small');
      confirm.addEventListener('click', (e) => {
        e.stopPropagation();
        select(item.id);
        if (item.pending) {
          placePendingItem(item);
        } else if (item.review_required) {
          item.review_required = false;
          item.characteristic.warnings = (item.characteristic.warnings || []).filter((w) => !w.startsWith('OCR 识别待'));
          state.revision++;
        }
        drawTable(); drawOverlay(currentPage()); updateChrome(state.drawing);
      });
      review.appendChild(confirm);
      lim.appendChild(review);
    }

    const box = document.createElement('input');
    box.type = 'checkbox';
    box.checked = item.include;
    box.disabled = !!item.pending || !!item.review_required;
    box.title = 'Include on the inspection report';
    box.addEventListener('change', () => {
      item.include = box.checked;
      state.revision++;
      tr.classList.toggle('excluded', !box.checked);
      drawOverlay(currentPage());
      updateChrome(state.drawing);
    });
    const inc = document.createElement('td');
    inc.className = 'c';
    inc.appendChild(box);

    tr.append(num, req, lim, inc);
    tr.addEventListener('click', (e) => {
      if (e.target === box || e.target === req) return;
      focusItem(item.id);
    });
    tbody.appendChild(tr);
  }
}

function cell(tag, text, cls) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  el.textContent = text;
  return el;
}

function updateChrome(d) {
  const total = d.items.length;
  const inspectable = d.items.filter((i) => i.include && !i.pending && !i.review_required).length;
  $('count').textContent = `${inspectable} of ${total} inspectable`;
  drawPendingList();
  drawConflictList();

  const warnings = [];
  for (const it of d.items) {
    for (const w of it.characteristic.warnings || []) warnings.push(`#${it.number} ${w}`);
    for (const w of it.issues || []) warnings.push(`#${it.number} ${w}`);
  }
  const box = $('warnings');
  if (warnings.length) {
    box.hidden = false;
    box.innerHTML = `<strong>Review before sign-off</strong><ul>${
      warnings.map((w) => `<li>${escapeHTML(w)}</li>`).join('')}</ul>`;
  } else {
    box.hidden = true;
  }

  const pager = document.querySelector('.pager');
  pager.hidden = d.pages.length < 2;
  $('pageLabel').textContent = `${state.page + 1} / ${d.pages.length}`;
  $('prev').disabled = state.page === 0;
  $('next').disabled = state.page >= d.pages.length - 1;
  $('exportPdf').disabled = !state.bytes || state.exporting;
  $('exportPdf').title = '导出原图和当前已画出的全部气泡；包含位置冲突项，待放置项不绘入 PDF';
  $('deleteBalloon').disabled = !findItem(state.selected);
  const edited = d.items.some((i) => i.manual || i.anchor);
  $('tidy').disabled = edited;
  $('tidy').title = edited ? '保留手工标注的位置；请直接拖动调整' : 'Re-solve balloon placement for this sheet';
  $('reparse').disabled = state.noText || edited || d.pages.some((p) => p.strict);
  $('autoBalloon').disabled = !state.pdf || state.busy;
  $('skillBalloon').disabled = !state.pdf || state.busy;
  $('autoLayout').disabled = !d.items.length || state.busy;
  $('renumber').disabled = !d.items.length || state.busy;
  $('applySize').disabled = !d.items.length || state.busy;
  $('showOcr').disabled = state.ocrTexts.length === 0 || state.busy;
  $('showOcr').setAttribute('aria-pressed', String(state.showOcr));
  $('exportSvg').disabled = d.pages.some((p) => p.strict);
  $('exportSvg').title = d.pages.some((p) => p.strict) ? 'OCR 图纸请导出 PDF；SVG 不包含完整原图' : '';
  const help = $('manualHelp');
  help.hidden = !state.pdf;
  help.textContent = `${state.noText ? '此 PDF 没有文字层，可点击“本地自动标注”。' : ''}OCR 图像仅发往运行本工具的服务器。点击表格编号查看原图局部并核对尺寸；小数点、公差和符号可能误读或漏识别。拖圆圈或线端蓝点调整；点击“位置冲突”定位问题。可随时导出当前已画出的标注，待放置项不绘入 PDF。刷新会丢失编辑，请及时导出；导出的 PDF 重新打开可继续编辑。`;
  if (d.pages.some((p) => p.strict) && !state.busy) {
    $('ocrStatus').hidden = false;
    const status = $('ocrStatus');
    status.textContent = `识别文字区域 ${state.ocrTexts.length}；尺寸候选 ${total}；已放置 ${d.items.filter((i) => !i.pending).length}；`;
    const pending = cell('button', `待放置 ${d.items.filter((i) => i.pending).length}`, 'link');
    pending.id = 'pendingStatus';
    pending.title = '展开待放置列表，点击条目定位原图尺寸';
    pending.setAttribute('aria-expanded', String(state.showPending));
    pending.addEventListener('click', togglePending);
    const conflicts = cell('button', `位置冲突 ${d.items.filter((i) => !i.pending && !i.clean).length}`, 'link');
    conflicts.id = 'conflictStatus';
    conflicts.setAttribute('aria-expanded', String(state.showConflicts));
    conflicts.setAttribute('aria-controls', 'conflictList');
    conflicts.addEventListener('click', toggleConflicts);
    status.append(pending, document.createTextNode('；'), conflicts, document.createTextNode(`；内容待核对 ${d.items.filter((i) => i.review_required).length}。未识别内容不会自动补齐。${state.ocrError}`));
  }
  if (state.busy) {
    for (const el of document.querySelectorAll('.bar button, .bar input, .bar select, #selectionPreview button, #selectionPreview input, #reparse, #loadDemo')) el.disabled = true;
  }
  $('exportPdf').disabled = !state.bytes || state.exporting;
}

// ----------------------------------------------------------------- selection

function togglePending() {
  if (state.busy) return;
  state.showPending = !state.showPending;
  if (state.drawing) updateChrome(state.drawing);
  if (state.showPending) $('pendingList').closest('.panel').scrollTo({ top: 0, behavior: 'instant' });
}

function toggleConflicts() {
  if (state.busy) return;
  state.showConflicts = !state.showConflicts;
  if (state.drawing) updateChrome(state.drawing);
  if (state.showConflicts) $('conflictList').scrollIntoView({ block: 'nearest' });
}

function drawConflictList() {
  const conflicts=(state.drawing?.items || []).filter((i)=>!i.pending&&!i.clean);
  const toggle=$('conflictToggle'), list=$('conflictList'), scrollTop=list.scrollTop;
  toggle.textContent=`位置冲突 ${conflicts.length}`;
  toggle.disabled=state.busy||(!conflicts.length&&!state.showConflicts);
  toggle.setAttribute('aria-expanded',String(state.showConflicts));
  list.hidden=!state.showConflicts;list.replaceChildren();
  if(!state.showConflicts)return;
  list.appendChild(cell('strong',`全部页位置冲突 ${conflicts.length}`));
  list.appendChild(cell('p',conflicts.length?'点击条目定位标注，查看原因；可拖动调整或点击“自动调整位置”。':'无位置冲突。','pending-hint'));
  for(const item of conflicts){
    const locate=cell('button',`#${item.number} · ${item.requirement||item.source.text||'未填写'} · 第 ${item.page+1} 页：${(item.issues||[]).join('；')||'位置尚未通过检查'}`,'btn small pending-locate conflict-locate');
    locate.dataset.id=item.id;locate.disabled=state.busy;
    locate.setAttribute('aria-pressed',String(state.selected===item.id));
    locate.addEventListener('click',()=>focusItem(item.id,{balloon:true}));
    list.appendChild(locate);
  }
  list.scrollTop=scrollTop;
}

function drawPendingList() {
  const pending = (state.drawing?.items || []).filter((i) => i.pending);
  const toggle = $('pendingToggle'), list = $('pendingList');
  const scrollTop = list.scrollTop;
  toggle.textContent = `待放置 ${pending.length}`;
  toggle.disabled = state.busy || (!pending.length && !state.showPending);
  toggle.setAttribute('aria-expanded', String(state.showPending));
  list.hidden = !state.showPending;
  list.replaceChildren();
  if (!state.showPending) return;
  const toolbar=document.createElement('div'); toolbar.className='pending-toolbar';
  toolbar.appendChild(cell('strong',`全部页待放置 ${pending.length}`));
  const all=cell('button','一键放置全部','btn small primary'); all.id='placeAllPending';
  all.disabled=state.busy||!pending.length; all.addEventListener('click',placeAllPending);
  toolbar.appendChild(all); list.appendChild(toolbar);
  list.appendChild(cell('p', pending.length ? '这些候选尚未画到图上。点击编号定位原图，点击“放置”生成可拖动气泡。' : '无待放置项。', 'pending-hint'));
  for (const item of pending) {
    const row = document.createElement('div'); row.className = 'pending-row'; row.dataset.id = item.id;
    const locate = cell('button', `#${item.number} · ${item.requirement || item.source.text || '未填写'} · 第 ${item.page + 1} 页`, 'btn small pending-locate');
    locate.setAttribute('aria-pressed', String(state.selected === item.id));
    locate.disabled = state.busy;
    locate.addEventListener('click', () => focusItem(item.id));
    const place = cell('button', '放置', 'btn small pending-place');
    place.disabled = state.busy;
    place.addEventListener('click', async () => {
      if (await focusItem(item.id) && item.pending && !state.busy) placePendingItem(item);
    });
    row.append(locate, place); list.appendChild(row);
  }
  list.scrollTop = scrollTop;
}

async function focusItem(id, { balloon = false } = {}) {
  const item = findItem(id);
  if (!item || state.busy) return;
  if (state.page !== item.page) {
    setBusy(true);
    try {
      state.page = item.page;
      await renderPage();
    } catch (err) {
      toast(`定位失败：${err.message}`, true);
      return false;
    } finally { setBusy(false); }
  }
  select(id, { scrollTable: false });
  // Scroll only the drawing pane, preserving the independent pending list.
  const box = item.source.box, svg = $('overlay'), point = svg.createSVGPoint();
  point.x = balloon && !item.pending ? item.balloon.c.x : box.x + box.w / 2;
  point.y = balloon && !item.pending ? item.balloon.c.y : box.y + box.h / 2;
  const screen = point.matrixTransform(svg.getScreenCTM()), stage = $('stage'), bounds = stage.getBoundingClientRect();
  stage.scrollTo({ left: stage.scrollLeft + screen.x - bounds.left - stage.clientWidth / 2,
    top: stage.scrollTop + screen.y - bounds.top - stage.clientHeight / 2, behavior: 'instant' });
  return true;
}

function stagePendingItem(item) {
  if (!item.pending) return;
  item.pending = false;
  const box = item.source.box, page = state.drawing.pages.find((p)=>p.index===item.page), r = currentRadius();
  item.balloon = { c: { x: clamp(box.x + box.w + 32, r, page.width - r), y: clamp(box.y - 25, r, page.height - r) }, r };
  item.leader = leaderFor(box, item.balloon, item.anchor, page.strict);
  item.clean = false;
}

function placePendingItem(item) {
  if (state.busy || !item.pending) return;
  stagePendingItem(item);
  state.revision++;
  drawOverlay(currentPage()); drawTable(); updateChrome(state.drawing);
  select(item.id, { scrollTable: false });
  validatePositions();
}

async function placeAllPending() {
  if (state.busy || !state.drawing) return;
  const pending=state.drawing.items.filter((i)=>i.pending);
  if (!pending.length) return;
  setBusy(true);
  try {
    pending.forEach(stagePendingItem); state.revision++;
    drawOverlay(currentPage()); drawTable();
    await validatePositions();
    const conflicts=state.drawing.items.filter((i)=>!i.pending&&!i.clean).length;
    toast(`已放置 ${pending.length} 个候选（全部页）；${conflicts} 个位置冲突，可点击“自动调整位置”处理。`);
  } finally { setBusy(false); }
}

function select(id, { scrollTable = true } = {}) {
  state.selected = id;
  $('deleteBalloon').disabled = !findItem(id);
  for (const el of document.querySelectorAll('.balloon')) {
    el.classList.toggle('selected', el.dataset.id === id);
  }
  for (const tr of document.querySelectorAll('#tbody tr')) {
    const on = tr.dataset.id === id;
    tr.classList.toggle('selected', on);
    if (on && scrollTable) tr.scrollIntoView({ block: 'nearest' });
  }
  const item = findItem(id);
  $('selectionPreview').hidden = !item || !state.pdf;
  if (item && state.pdf) {
    const source = $('canvas'), box = item.source.box, page = currentPage();
    const x = Math.max(0, box.x - 15), y = Math.max(0, box.y - 15);
    const w = Math.min(page.width - x, box.w + 30), h = Math.min(page.height - y, box.h + 30);
    const crop = document.createElement('canvas');
    crop.width = Math.max(1, Math.round(w * 3)); crop.height = Math.max(1, Math.round(h * 3));
    crop.getContext('2d').drawImage(source, x / page.width * source.width, y / page.height * source.height,
      w / page.width * source.width, h / page.height * source.height, 0, 0, crop.width, crop.height);
    $('previewImage').src = crop.toDataURL();
    $('previewLabel').textContent = `${item.pending ? '待放置 ' : ''}#${item.number} 原文：${item.source.raw_text || item.source.text || '手动标注'}${item.source.alternatives?.length ? `；其他识别：${item.source.alternatives.join(' / ')}` : ''}${!item.pending&&!item.clean ? `；位置冲突：${(item.issues||[]).join('；')||'位置尚未通过检查'}` : ''}`;
    $('engineeringValue').value=(item.requirement || item.source.text || '').replace(/\s+REF$/i,'');
  }
  if (state.drawing) drawOverlay(currentPage());
  drawPendingList();
  drawConflictList();
}

// ------------------------------------------------------------------ dragging

// A balloon the solver could not place cleanly still has to end up somewhere
// sensible, and the fastest fix is a human dragging it. The leader is recomputed
// as it moves so it keeps terminating on the circle's edge rather than its
// centre — the same rule the solver uses.
function installDrag() {
  const overlay = $('overlay');
  let drag = null;

  overlay.addEventListener('pointerdown', (e) => {
    if (state.busy || e.button !== 0) return;
    const g = e.target.closest('.balloon');
    if (!g) {
      if (state.adding && state.drawing) addBalloon(toSVG(e));
      else select(null);
      return;
    }
    const item = findItem(g.dataset.id);
    if (!item) return;

    const p = toSVG(e);
    const anchor = e.target.classList.contains('tip');
    drag = { g, item, anchor, dx: item.balloon.c.x - p.x, dy: item.balloon.c.y - p.y, moved: false };
    overlay.setPointerCapture(e.pointerId);
    select(item.id);
    drag.g = overlay.querySelector(`.balloon[data-id="${CSS.escape(item.id)}"]`);
    e.preventDefault();
  });

  overlay.addEventListener('pointermove', (e) => {
    if (!drag) return;
    const p = toSVG(e);
    const page = currentPage();
    if (drag.anchor) {
      drag.item.anchor = { x: clamp(p.x, 0, page.width), y: clamp(p.y, 0, page.height) };
    } else {
      const r = drag.item.balloon.r;
      drag.item.balloon.c.x = clamp(p.x + drag.dx, r, page.width - r);
      drag.item.balloon.c.y = clamp(p.y + drag.dy, r, page.height - r);
    }
    drag.item.leader = leaderFor(drag.item.source.box, drag.item.balloon, drag.item.anchor);
    drag.moved = true;

    // Once a human has placed it, the solver's complaint no longer applies.
    drag.item.clean = true;
    drag.item.issues = [];
    state.revision++;

    drag.g.replaceWith(balloonNode(drag.item));
    drag.g = overlay.querySelector(`.balloon[data-id="${CSS.escape(drag.item.id)}"]`);
  });

  const end = (e) => {
    if (!drag) return;
    if (drag.moved) { updateChrome(state.drawing); validatePositions(); }
    try { overlay.releasePointerCapture(e.pointerId); } catch { /* already gone */ }
    drag = null;
  };
  overlay.addEventListener('pointerup', end);
  overlay.addEventListener('pointercancel', end);
}

function toSVG(e) {
  const svg = $('overlay');
  const pt = svg.createSVGPoint();
  pt.x = e.clientX;
  pt.y = e.clientY;
  return pt.matrixTransform(svg.getScreenCTM().inverse());
}

// Mirrors layout.leaderFor on the Go side.
function leaderFor(box, circle, anchor, strict=currentPage()?.strict) {
  const center = { x: box.x + box.w / 2, y: box.y + box.h / 2 };
  let a = anchor || center;
  if (!anchor && strict && (box.w || box.h)) {
    const dx = circle.c.x - center.x, dy = circle.c.y - center.y;
    const t = Math.min(dx ? (box.w / 2 + 2) / Math.abs(dx) : Infinity, dy ? (box.h / 2 + 2) / Math.abs(dy) : Infinity);
    a = { x: center.x + dx * t, y: center.y + dy * t };
  }
  const dx = circle.c.x - a.x;
  const dy = circle.c.y - a.y;
  const len = Math.hypot(dx, dy) || 1;
  return {
    a,
    b: { x: circle.c.x - (dx / len) * circle.r, y: circle.c.y - (dy / len) * circle.r },
  };
}

// -------------------------------------------------------------------- actions

function setBusy(on) {
  state.busy = on;
  document.body.classList.toggle('processing', on);
  for (const el of document.querySelectorAll('.bar button, .bar input, .bar select, #selectionPreview button, #selectionPreview input, #reparse, #loadDemo')) el.disabled = on;
  if (!on && state.drawing) updateChrome(state.drawing);
  $('exportPdf').disabled = !state.bytes || state.exporting;
}

async function autoBalloon() {
  if (!state.pdf || state.busy) return;
  if (state.ocrDone.size === state.pdf.numPages) return toast('已有识别结果。请使用“自动调整位置”；重新打开 PDF 可重新识别。');
  state.ocrError = '';
  setAdding(false);
  setBusy(true);
  const status = $('ocrStatus'); status.hidden = false;
  try {
    for (const meta of state.drawing.pages) {
      if (state.ocrDone.has(meta.index)) continue;
      status.textContent = `正在本地识别第 ${meta.index + 1} / ${state.pdf.numPages} 页…每页可能需要几十秒，请保持页面开启。`;
      const pdfPage = await state.pdf.getPage(meta.index + 1);
      const scale = Math.min(3, 4000 / Math.max(meta.width, meta.height), Math.sqrt(12e6 / (meta.width * meta.height)));
      const vp = pdfPage.getViewport({ scale });
      const canvas = document.createElement('canvas'); canvas.width = vp.width; canvas.height = vp.height;
      await pdfPage.render({ canvasContext: canvas.getContext('2d'), viewport: vp }).promise;
      const png = await new Promise((resolve) => canvas.toBlob(resolve, 'image/png'));
      if (!png) throw new Error('无法生成页面图像');
      const res = await fetch('/api/ocr', { method: 'POST', headers: { 'Content-Type': 'image/png' }, body: png });
      if (!res.ok) { const e = await res.json(); throw new Error(e.error || `OCR 请求失败 ${res.status}`); }
      const recognized = await res.json();
      const box = (b) => ({ x: b.x * meta.width, y: b.y * meta.height, w: b.w * meta.width, h: b.h * meta.height });
      const texts = recognized.texts.map((t) => ({ text: t.text, raw_text: t.raw_text, page: meta.index, box: box(t.box),
        ocr: true, confidence: t.confidence, rotation: t.rotation, alternatives: t.alternatives || [] }));
      const pageMeta = { index: meta.index, width: meta.width, height: meta.height, strict: true,
        obstacles: recognized.obstacles.map(box), text_obstacles: texts.map((t) => t.box) };
      const built = await post('/api/build', { drawing: { id: 'local', pages: [pageMeta],balloon_radius:currentRadius() }, texts, unit: 'mm' });
      if (!built) throw new Error('生成标注失败；已完成页面会保留');
      const actual = state.drawing.pages.find((p) => p.index === meta.index);
      Object.assign(actual, built.pages[0]);
      const added = (built.items || []).map((i) => ({ ...i, id: `ocr-${crypto.randomUUID()}` }));
      state.drawing.items = state.drawing.items.filter((i) => i.page !== meta.index || i.manual).concat(added);
      state.ocrTexts = state.ocrTexts.filter((t) => t.page !== meta.index).concat(texts);
      state.drawing.items.sort((a, b) => a.page - b.page || a.number - b.number);
      state.drawing.items.forEach((i, n) => { i.number = n + 1; });
      const placed = await post('/api/layout', { drawing: state.drawing, page: meta.index });
      if (!placed) throw new Error('自动排版失败；请重试自动调整位置');
      state.drawing = { ...placed, items: placed.items || [] };
      state.revision++;
      state.ocrDone.add(meta.index);
      canvas.width = canvas.height = 1;
    }
    state.page = 0; state.selected = null;
    state.showPending = state.drawing.items.some((i) => i.pending);
    await renderPage();
    toast('本地识别完成，可导出当前标注；点击“待放置”或“位置冲突”查看待处理项。');
  } catch (err) {
    state.ocrError = `识别未全部完成（累计完成 ${state.ocrDone.size} 页）：${err.message}。可再次点击本地自动标注重试未完成页。`;
    status.textContent = state.ocrError;
    toast(err.message, true);
    await renderPage();
  } finally {
    setBusy(false);
  }
}

// Lay out every candidate with the engineering-drawing-balloon skill on the server.
// Candidates come from this editor (text layer or local OCR); the skill adds
// any text-layer callouts it reads itself and returns positions, which become
// ordinary editable balloons here.
async function skillBalloon() {
  if (!state.pdf || state.busy) return;
  if (state.noText && state.ocrDone.size < state.pdf.numPages) {
    toast('此 PDF 没有文字层，先进行本地识别，再交给 Skill 排版…');
    await autoBalloon();
    if (state.ocrDone.size < state.pdf.numPages) return;
  }
  setAdding(false);
  setBusy(true);
  const status = $('ocrStatus'); status.hidden = false;
  status.textContent = '正在调用 engineering-drawing-balloon Skill 排版全部页面…';
  try {
    // The skill works in unrotated PDF points with a top-left origin.
    const frames = [];
    for (let n = 1; n <= state.pdf.numPages; n++) {
      const page = await state.pdf.getPage(n);
      frames.push({ view: page.getViewport({ scale: 1 }), flat: page.getViewport({ scale: 1, rotation: 0 }) });
    }
    const toSkill = (p, f) => f.flat.convertToViewportPoint(...f.view.convertToPdfPoint(p.x, p.y));
    const fromSkill = ([u, v], f) => {
      const [x, y] = f.view.convertToViewportPoint(...f.flat.convertToPdfPoint(u, v));
      return { x, y };
    };
    const toBox = (bbox, f) => {
      const a = fromSkill([bbox[0], bbox[1]], f), b = fromSkill([bbox[2], bbox[3]], f);
      return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), w: Math.abs(a.x - b.x), h: Math.abs(a.y - b.y) };
    };
    const candidates = state.drawing.items.filter((i) => i.source.box.w > 0 && i.source.box.h > 0);
    const features = candidates.map((item) => {
      const f = frames[item.page], b = item.source.box;
      const [x0, y0] = toSkill({ x: b.x, y: b.y }, f), [x1, y1] = toSkill({ x: b.x + b.w, y: b.y + b.h }, f);
      return { page: item.page + 1, text: item.requirement || item.source.text, source: `item:${item.id}`,
        bbox: [Math.min(x0, x1), Math.min(y0, y1), Math.max(x0, x1), Math.max(y0, y1)] };
    });
    const form = new FormData();
    form.append('pdf', new Blob([state.bytes], { type: 'application/pdf' }), 'drawing.pdf');
    form.append('features', JSON.stringify({ features }));
    const res = await fetch('/api/skill', { method: 'POST', body: form });
    const result = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(result.error || `Skill 请求失败 ${res.status}`);

    const radius = Number(result.radius) || currentRadius();
    const byId = new Map(candidates.map((i) => [i.id, i]));
    const overlaps = (a, b) => a.x < b.x + b.w + 2 && b.x < a.x + a.w + 2 && a.y < b.y + b.h + 2 && b.y < a.y + a.h + 2;
    const used = new Set(), placed = [], unplaced = [];
    let blocked = 0;
    for (const a of result.annotations || []) {
      if (a.status === 'REQUIRES_AI_OCR') { blocked++; continue; }
      const f = frames[a.page - 1], box = toBox(a.bbox, f);
      const source = String(a.source);
      let item = source.startsWith('item:') ? byId.get(source.slice(5)) : null;
      if (!item) {
        // Text the skill read from the PDF itself. Where it overlaps one of
        // ours it is the same callout: one balloon for that item, not two.
        const twin = candidates.find((c) => c.page === a.page - 1 && overlaps(c.source.box, box));
        item = twin || await skillItem(a, box);
      }
      if (!item || used.has(item.id)) continue;
      used.add(item.id);
      if (a.status === 'placed_unverified') {
        item.pending = false;
        item.balloon = { c: fromSkill(a.bubble_center, f), r: radius };
        item.anchor = fromSkill(a.leader[1], f);
        item.leader = leaderFor(item.source.box, item.balloon, item.anchor);
        item.clean = true; item.issues = [];
        placed.push(item);
      } else {
        item.pending = true;
        unplaced.push(item);
      }
    }
    // Keep the skill's numbering; items it did not handle follow.
    const rest = state.drawing.items.filter((i) => !used.has(i.id));
    state.drawing.items = [...placed, ...unplaced, ...rest];
    state.drawing.items.forEach((item, n) => { item.number = n + 1; });
    state.drawing.balloon_radius = radius;
    state.revision++; state.selected = null;
    state.showPending = unplaced.length > 0;
    status.hidden = true; // renderPage shows it again for OCR pages
    await renderPage();
    toast(`Skill 已排版 ${placed.length} 个标注；${unplaced.length} 个需手动放置${blocked ? `；${blocked} 页缺少可识别文字` : ''}。尺寸内容仍需核对。`);
  } catch (err) {
    status.textContent = `Skill 标注失败：${err.message}`;
    toast(err.message, true);
  } finally {
    setBusy(false);
  }
  validatePositions();
}

// A callout only the skill found becomes a new item, parsed like a typed one.
async function skillItem(a, box) {
  const res = await post('/api/parse', parseBody(a.text, false));
  if (!res) return null;
  const item = {
    id: `skill-${crypto.randomUUID()}`, page: a.page - 1, number: 0, clean: true, issues: [],
    source: { text: a.text, page: a.page - 1, box },
    characteristic: res.characteristic, requirement: res.requirement, limits: res.limits,
    designator: res.designator, include: res.inspectable,
  };
  state.drawing.items.push(item);
  return item;
}

function parseBody(text, ocr) {
  return {
    text, ocr,
    tolerances: {
      1: num($('tol1').value, 0.2),
      2: num($('tol2').value, 0.1),
      3: num($('tol3').value, 0.05),
    },
    angular: num($('tolAng').value, 0.5),
    unit: 'mm',
  };
}

async function autoLayout() {
  if (!state.drawing || state.busy) return;
  setBusy(true);
  try {
    const res = await post('/api/layout', { drawing: state.drawing, page: state.page });
    if (!res) return;
    state.drawing = { ...res, items: res.items || [] };
    state.revision++;
    await renderPage();
    toast('已根据本页现有标注调整：安全位置优先保留；无法解决的冲突仍会提示。');
  } finally { setBusy(false); }
}

async function validatePositions() {
  if (!state.drawing) return true;
  const token = ++state.validationToken, revision = state.revision;
  state.validating = true; updateChrome(state.drawing);
  const res = await post('/api/validate', { drawing: state.drawing, page: state.page });
  if (token !== state.validationToken) return false;
  if (revision !== state.revision) return validatePositions();
  if (!res) return false; // Remain blocked if the check could not run.
  for (const check of res.items || []) {
    const item = findItem(check.id);
    if (item) { item.clean = check.clean; item.issues = check.issues || []; }
  }
  state.validating = false;
  drawOverlay(currentPage()); drawTable(); updateChrome(state.drawing);
  return !state.drawing.items.some((i) => i.pending || !i.clean);
}

function currentRadius() { return state.drawing?.balloon_radius || BALLOON_STYLE.radius; }

async function applySize() {
  if (!state.drawing || state.busy) return;
  const radius=Number($('balloonSize').value);
  if (![6,9,12,15].includes(radius)) return;
  setBusy(true);
  try {
    const drawing=structuredClone(state.drawing); drawing.balloon_radius=radius;
    drawing.items.forEach((i)=>{if(!i.pending)i.balloon.r=radius;});
    const res=await post('/api/layout',{drawing,page:state.page,all_pages:true});
    if(!res)return;
    state.drawing={...res,items:res.items||[]};state.revision++;
    await renderPage();
    const conflicts=state.drawing.items.filter((i)=>i.pending||!i.clean).length;
    toast(`全部页已统一大小并调整位置；${conflicts} 项仍需处理。`);
  } finally {setBusy(false);}
}

const clamp = (n, min, max) => Math.min(max, Math.max(min, n));

function setAdding(on) {
  state.adding = on;
  $('addBalloon').setAttribute('aria-pressed', String(on));
  $('overlay').classList.toggle('adding', on);
}

function addBalloon(p) {
  const page = currentPage();
  if (p.x < 0 || p.y < 0 || p.x > page.width || p.y > page.height) return;
  const r = currentRadius();
  const item = {
    id: `manual-${crypto.randomUUID()}`, page: state.page,
    number: Math.max(0, ...state.drawing.items.map((i) => i.number)) + 1,
    manual: true, include: false, clean: true, issues: [],
    source: { text: '', page: state.page, box: { x: p.x, y: p.y, w: 0, h: 0 } },
    characteristic: { warnings: [] }, requirement: '', limits: '', designator: '',
    balloon: { c: { x: clamp(p.x + 32, r, page.width - r), y: clamp(p.y - 28, r, page.height - r) }, r },
  };
  item.leader = leaderFor(item.source.box, item.balloon);
  state.drawing.items.push(item);
  state.revision++;
  state.selected = item.id;
  drawOverlay(page);
  drawTable();
  updateChrome(state.drawing);
  validatePositions();
}

function renumber() {
  if (!state.drawing || state.busy || !state.drawing.items.length) return;
  const position = (item) => item.pending
    ? { x: item.source.box.x + item.source.box.w / 2, y: item.source.box.y + item.source.box.h / 2 }
    : item.balloon.c;
  const items = [...state.drawing.items].sort((a, b) =>
    Number(!!a.pending) - Number(!!b.pending) || a.page - b.page ||
    position(a).y - position(b).y || position(a).x - position(b).x || a.id.localeCompare(b.id));
  const ordered = [];
  // Treat centers within one radius of the row's top as the same row. Using
  // the first center avoids a chain of small offsets merging separate rows.
  for (let start = 0; start < items.length;) {
    const first = items[start], top = position(first).y;
    let end = start + 1;
    while (end < items.length && items[end].page === first.page &&
      !!items[end].pending === !!first.pending && position(items[end]).y - top <= currentRadius()) end++;
    ordered.push(...items.slice(start, end).sort((a, b) =>
      position(a).x - position(b).x || position(a).y - position(b).y || a.id.localeCompare(b.id)));
    start = end;
  }
  state.drawing.items = ordered;
  state.drawing.items.forEach((item, index) => { item.number = index + 1; });
  state.revision++;
  drawOverlay(currentPage());
  drawTable();
  select(state.selected, { scrollTable: false });
  updateChrome(state.drawing);
  toast(`已按气泡位置从左上到右下连续编号为 1–${state.drawing.items.length}；待放置项接续在后。`);
}

function deleteSelected() {
  if (!findItem(state.selected)) return;
  state.drawing.items = state.drawing.items.filter((i) => i.id !== state.selected);
  state.drawing.items.forEach((item, i) => { item.number = i + 1; });
  state.selected = null;
  $('selectionPreview').hidden = true;
  state.revision++;
  drawOverlay(currentPage());
  drawTable();
  updateChrome(state.drawing);
  validatePositions();
}

async function exportPDF() {
  if (!state.bytes || !state.pdf || state.exporting) return;
  const drawing = structuredClone(state.drawing), source=state.bytes, pdf=state.pdf, filename=state.filename;
  state.exporting=true; $('exportPdf').disabled=true;
  try {
    const viewports = [];
    for (let n = 1; n <= pdf.numPages; n++) {
      viewports.push((await pdf.getPage(n)).getViewport({ scale: 1 }));
    }
    syncMeta();
    const edit = { filename, drawing, ocr_texts: state.ocrTexts, ocr_done: [...state.ocrDone],
      meta: { partNumber: $('partNumber').value, partName: $('partName').value, revision: $('revision').value, serial: $('serial').value } };
    const bytes = await balloonPDF(source, drawing, viewports, edit);
    saveBlob(new Blob([bytes], { type: 'application/pdf' }), filename.replace(/\.pdf$/i, '') + '-ballooned.pdf');
    const placed=drawing.items.filter((i)=>!i.pending).length, pending=drawing.items.length-placed;
    toast(`已导出 PDF：${placed} 个已放置标注（含冲突项）；${pending} 个待放置项未绘入但随编辑数据保存。重新打开该 PDF 可继续编辑。`);
  } catch (err) {
    toast(`PDF 导出失败：${err.message}`, true);
  } finally {
    state.exporting=false;
    if(state.drawing)updateChrome(state.drawing);
  }
}

function saveBlob(blob, name) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function reparse(id, text) {
  const item = findItem(id);
  if (!item || text === item.requirement) return;
  if (!text) {
    if (item.manual) {
      item.characteristic = { warnings: [] };
      item.requirement = '';
      item.limits = '';
      item.designator = '';
      item.include = false;
    }
    drawTable();
    drawOverlay(currentPage());
    updateChrome(state.drawing);
    return;
  }

  const res = await post('/api/parse', parseBody(text, !!item.source.ocr));
  if (!res) return;

  item.characteristic = res.characteristic;
  item.requirement = res.requirement;
  item.limits = res.limits;
  item.designator = res.designator;
  item.include = res.inspectable;
  item.review_required = false;
  state.revision++;

  drawTable();
  drawOverlay(currentPage());
  updateChrome(state.drawing);
  toast(`#${item.number} re-read as ${res.requirement}`);
}

async function tidy() {
  if (!state.drawing) return;
  const res = await post('/api/layout', { drawing: state.drawing, page: state.page });
  if (!res) return;
  state.drawing = res;
  drawOverlay(currentPage());
  drawTable();
  updateChrome(state.drawing);
  toast('Balloons re-placed');
}

async function download(path, fallbackName) {
  if (!state.drawing) return toast('Open a drawing first', true);

  syncMeta();
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      drawing: state.drawing,
      page: state.page,
      meta: {
        PartNumber: $('partNumber').value,
        PartName: $('partName').value,
        SerialNumber: $('serial').value,
        Revision: $('revision').value,
        DrawingNo: $('partNumber').value,
      },
    }),
  });
  if (!res.ok) return toast(`Export failed (${res.status})`, true);

  const name = filenameFrom(res.headers.get('Content-Disposition')) || fallbackName;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
  toast(`Saved ${name}`);
}

function filenameFrom(header) {
  const m = /filename="([^"]+)"/.exec(header || '');
  return m ? m[1] : null;
}

function syncMeta() {
  if (!state.drawing) return;
  state.drawing.part_number = $('partNumber').value;
  state.drawing.name = $('partName').value;
  state.drawing.revision = $('revision').value;
}

// --------------------------------------------------------------------- utils

const itemsOnPage = () => (state.drawing?.items || []).filter((i) => i.page === state.page);
const currentPage = () => state.drawing.pages.find((p) => p.index === state.page) || state.drawing.pages[0];
const findItem = (id) => (state.drawing?.items || []).find((i) => i.id === id);
const num = (v, fallback) => (Number.isFinite(parseFloat(v)) ? parseFloat(v) : fallback);

function escapeHTML(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

async function post(path, body) {
  try {
    const res = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      toast(err.error || `Request failed (${res.status})`, true);
      return null;
    }
    return res.json();
  } catch (e) {
    toast(String(e), true);
    return null;
  }
}

let toastTimer;
function toast(msg, isError = false) {
  const el = $('toast');
  el.textContent = msg;
  el.classList.toggle('err', isError);
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, isError ? 6000 : 2600);
}

// ---------------------------------------------------------------------- wire

function init() {
  installDrag();
  $('pendingToggle').addEventListener('click', togglePending);
  $('conflictToggle').addEventListener('click', toggleConflicts);
  $('addBalloon').addEventListener('click', () => {
    if (!state.drawing) return toast('请先打开 PDF', true);
    setAdding(!state.adding);
  });
  $('deleteBalloon').addEventListener('click', deleteSelected);
  $('exportPdf').addEventListener('click', exportPDF);
  $('autoBalloon').addEventListener('click', autoBalloon);
  $('skillBalloon').addEventListener('click', skillBalloon);
  $('autoLayout').addEventListener('click', autoLayout);
  $('renumber').addEventListener('click', renumber);
  $('applySize').addEventListener('click',applySize);
  $('applyEngineering').addEventListener('click',()=>{
    if(!state.busy && state.selected) reparse(state.selected,$('engineeringValue').value.trim());
  });
  $('engineeringValue').addEventListener('keydown',(e)=>{
    if(e.key==='Enter'){e.preventDefault();$('applyEngineering').click();}
  });
  for(const button of document.querySelectorAll('[data-mark]'))button.addEventListener('click',()=>{
    const input=$('engineeringValue'),mark=button.dataset.mark;
    let value=input.value.trim();
    if(mark==='()'){input.value=value.startsWith('(')&&value.endsWith(')')?value.slice(1,-1):`(${value})`;return;}
    const reference=value.startsWith('(')&&value.endsWith(')');
    if(reference)value=value.slice(1,-1);
    if(mark==='Ø'||mark==='R'){
      const qty=/^(\d+\s*[-xX×]\s*)(.*)$/.exec(value),prefix=qty?qty[1]:'';
      value=prefix+mark+(qty?qty[2]:value).replace(/^[⌀ØΦφϕø∅R]\s*/,'');
    }else if(mark==='°'){if(!/[°º˚]/.test(value))value+='°';}
    else value+=' ±';
    input.value=reference?`(${value})`:value;input.focus();
  });
  $('showOcr').addEventListener('click', () => {
    state.showOcr = !state.showOcr;
    drawOverlay(currentPage()); updateChrome(state.drawing);
  });

  $('file').addEventListener('change', (e) => {
    if (e.target.files[0]) loadPDF(e.target.files[0]).catch((err) => toast(String(err), true));
  });
  $('loadDemo').addEventListener('click', () => loadDemo());

  const stage = $('stage');
  stage.addEventListener('dragover', (e) => { e.preventDefault(); stage.classList.add('dragover'); });
  stage.addEventListener('dragleave', () => stage.classList.remove('dragover'));
  stage.addEventListener('drop', (e) => {
    e.preventDefault();
    stage.classList.remove('dragover');
    const f = e.dataTransfer.files[0];
    if (f && f.type === 'application/pdf') loadPDF(f).catch((err) => toast(String(err), true));
    else toast('That is not a PDF', true);
  });

  $('prev').addEventListener('click', () => { state.page--; renderPage(); });
  $('next').addEventListener('click', () => { state.page++; renderPage(); });
  $('zoomIn').addEventListener('click', () => { state.zoom = Math.min(4, state.zoom * 1.25); renderPage(); });
  $('zoomOut').addEventListener('click', () => { state.zoom = Math.max(0.3, state.zoom / 1.25); renderPage(); });
  $('tidy').addEventListener('click', tidy);
  $('exportXlsx').addEventListener('click', () => download('/api/export.xlsx', 'inspection.xlsx'));
  $('exportSvg').addEventListener('click', () => download('/api/export.svg', 'drawing.svg'));

  $('reparse').addEventListener('click', async () => {
    if (!state.drawing) return toast('Open a drawing first', true);
    syncMeta();
    await build({
      id: state.drawing.id,
      name: state.drawing.name,
      part_number: state.drawing.part_number,
      revision: state.drawing.revision,
      balloon_radius: currentRadius(),
      pages: state.drawing.pages.map((p) => ({ index: p.index, width: p.width, height: p.height })),
    });
    await renderPage();
    toast('Drawing re-read with the new defaults');
  });

  for (const id of ['partNumber', 'partName', 'revision']) {
    $(id).addEventListener('change', syncMeta);
  }

  document.addEventListener('keydown', (e) => {
    if (state.busy) return;
    if (e.target.isContentEditable || e.target.tagName === 'INPUT') return;
    if (e.key === 'Escape') { setAdding(false); select(null); }
    if (e.key === 'Delete' || e.key === 'Backspace') { e.preventDefault(); deleteSelected(); }
    if (e.key === 'ArrowRight' && !$('next').disabled) { state.page++; renderPage(); }
    if (e.key === 'ArrowLeft' && !$('prev').disabled) { state.page--; renderPage(); }
  });
}

init();
