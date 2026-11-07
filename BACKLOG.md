# Backlog

Ordered. The top unchecked item is the next one to do.

A slice is "done" when it has tests, `go test ./...` passes, `go vet` is clean,
and the README's Known Limitations section still tells the truth.

## Correctness of what already exists

- [x] **Drag leader anchors independently of balloons.** Endpoints are retained
      during editing and in PDF export. Reloadable project state remains below.
- [x] **Explain drawings with no text layer.** Show a persistent message and
      enter manual balloon mode for scanned and vector-only prints.
- [ ] **SVG export covers only the first sheet.** The XLSX already covers all of
      them.

## Revision diff

The reason the tool exists: the customer issues revision D and the whole sheet
gets redone by hand.

- [ ] Load two revisions of a drawing and match characteristics between them by
      callout text and position.
- [ ] Report added, removed, and changed characteristics, with the old and new
      requirement side by side.
- [ ] Carry balloon numbers across a revision where the characteristic is
      unchanged, so a reviewer only re-inspects what actually moved. Placement
      is already deterministic, which is what makes this possible.

## Then

- [ ] Persist a project. The server is stateless and a reload loses the work.
- [ ] AS9102 Forms 1 and 2 (part accountability, materials).
- [ ] Replace the prose-filter allowlist with something that does not drop
      legitimate callouts containing unusual abbreviations.

## Not doing

- OCR. It is a real gap but it is a different project.
