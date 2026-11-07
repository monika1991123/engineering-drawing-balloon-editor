// Draw onto the original PDF, retaining its vectors and every page. Viewports
// from PDF.js account for PDF crop boxes, page rotation and nonzero origins.
export const BALLOON_STYLE = Object.freeze({ radius: 9, fontRatio: .95, lineWidth: .7, color: '#286195', tailHalfWidth: 1.5 });

// A narrow hollow tail joins the numbered circle to the callout. Its base is
// slightly inside the circle so the white circle hides the join cleanly.
export function leaderSides(leader, circle) {
	const ratio=circle.r/BALLOON_STYLE.radius;
  const dx = circle.c.x - leader.a.x, dy = circle.c.y - leader.a.y;
  const length = Math.hypot(dx, dy) || 1;
  const ux = dx / length, uy = dy / length;
  const base = { x: circle.c.x - ux * (circle.r - ratio), y: circle.c.y - uy * (circle.r - ratio) };
  return [
    { x: base.x - uy * BALLOON_STYLE.tailHalfWidth*ratio, y: base.y + ux * BALLOON_STYLE.tailHalfWidth*ratio },
    { x: base.x + uy * BALLOON_STYLE.tailHalfWidth*ratio, y: base.y - ux * BALLOON_STYLE.tailHalfWidth*ratio },
  ];
}

// An exported PDF carries the untouched source PDF and the editor state as
// attachments, so opening it again restores editable balloons instead of
// painting new ones over the flattened old ones.
export const EDIT_FORMAT = 'balloon-edit';
const EDIT_STATE = 'balloon-state.json', EDIT_SOURCE = 'balloon-source.pdf';

async function sha256(bytes) {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return null; // Not available outside secure contexts.
  const hash = new Uint8Array(await subtle.digest('SHA-256', bytes));
  return Array.from(hash, (b) => b.toString(16).padStart(2, '0')).join('');
}

// Returns { state, source } for a PDF this editor exported, otherwise null.
export async function readEditState(pdf) {
  const files = await pdf.getAttachments();
  const json = files?.[EDIT_STATE]?.content, source = files?.[EDIT_SOURCE]?.content;
  if (!json || !source) return null;
  const state = JSON.parse(new TextDecoder().decode(json));
  if (state?.format !== EDIT_FORMAT || state.version !== 1 || !Array.isArray(state.drawing?.items)) return null;
  const hash = await sha256(source);
  if (state.source_sha256 && hash && hash !== state.source_sha256) throw new Error('内嵌原图校验失败');
  return { state, source: new Uint8Array(source) };
}

export async function balloonPDF(bytes, drawing, viewports, edit = null) {
  const { PDFDocument, StandardFonts, rgb, degrees } = globalThis.PDFLib;
  const doc = await PDFDocument.load(bytes);
  const font = await doc.embedFont(StandardFonts.Helvetica);
  const blue = rgb(40 / 255, 97 / 255, 149 / 255);
  for (const item of drawing.items || []) {
    // Pending candidates have no visible position. Export the current drawing,
    // including conflicting placements, without inventing positions for them.
    if (item.pending) continue;
    const page = doc.getPage(item.page);
    const vp = viewports[item.page];
    const point = (p) => {
      const [x, y] = vp.convertToPdfPoint(p.x, p.y);
      return { x, y };
    };
    const scale = 1 / Math.hypot(vp.transform[0], vp.transform[1]);
    const lineWidth=BALLOON_STYLE.lineWidth*item.balloon.r/BALLOON_STYLE.radius;
    for (const side of leaderSides(item.leader, item.balloon)) {
      page.drawLine({ start: point(item.leader.a), end: point(side), thickness: lineWidth * scale, color: blue });
    }
    page.drawCircle({ ...point(item.balloon.c), size: item.balloon.r * scale,
      color: rgb(1, 1, 1), borderColor: blue, borderWidth: lineWidth * scale });
    const size = item.balloon.r * BALLOON_STYLE.fontRatio;
    const label = String(item.number);
    const width = font.widthOfTextAtSize(label, size);
    page.drawText(label, { ...point({ x: item.balloon.c.x - width / 2, y: item.balloon.c.y + size * 0.36 }),
      size: size * scale, font, color: blue, rotate: degrees(vp.rotation) });
  }
  if (edit) {
    const state = { ...edit, format: EDIT_FORMAT, version: 1, source_sha256: await sha256(bytes) };
    await doc.attach(bytes, EDIT_SOURCE, { mimeType: 'application/pdf', description: 'balloon 原始图纸（未标注）' });
    await doc.attach(new TextEncoder().encode(JSON.stringify(state)), EDIT_STATE,
      { mimeType: 'application/json', description: 'balloon 编辑状态' });
  }
  return doc.save();
}
