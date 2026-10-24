// Fast deterministic regressions, no browser/server required.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../src/pubcraftgui/web/static/app.js'), 'utf8');
const elements = new Map();
const context = vm.createContext({
  document: {
    activeElement: null,
    createElementNS: () => ({
      attrs: {}, style: {}, children: [],
      setAttribute(k, v) { this.attrs[k] = String(v); },
      appendChild(child) { this.children.push(child); },
    }),
  },
  pubState: { guides: { cut: true, safe: true }, printSettings: { trim: '6x9', bleed: true } },
  state: { book: { trim: '5x8', bleed: false } },
  $: id => {
    if (!elements.has(id)) elements.set(id, { classList: { remove() {} }, focus() {} });
    return elements.get(id);
  },
  updatePubStatusUI() {}, renderPubViewer() {},
});
vm.runInContext(source.slice(source.indexOf('function kdpGutterInches('), source.indexOf('function renderPubEbookView(')), context);
for (const scale of [0.5, 1, 2]) {
  for (const recto of [true, false]) {
    // 5x8 trim plus 0.125in outer bleed and 0.25in vertical bleed.
    const w = (360 + 9) * scale, h = (576 + 18) * scale;
    const svg = context.createPubGuidesSVG(w, h, recto, true, scale, 200);
    const [cut, safe] = svg.children;
    assert.equal(+cut.attrs.x, recto ? 0 : 9 * scale);
    assert.equal(+cut.attrs.y, 9 * scale);
    assert.equal(+cut.attrs.width, 360 * scale);
    assert.equal(+cut.attrs.height, 576 * scale);
    assert.equal(+safe.attrs.x, (recto ? 36 : 27) * scale);
  }
}
vm.runInContext(source.slice(source.indexOf('function openPublicationPreviewModal('), source.indexOf('function closePublicationPreviewModal(')), context);
context.openPublicationPreviewModal();
assert.equal(context.pubState.printSettings.trim, '6x9');
assert.equal(context.pubState.printSettings.bleed, true);
console.log('PASS: trim/bleed geometry, zoom, gutter and modal settings persistence');
