// Exercise the browser scope's zoom and coordinate mapping without a browser.
//
// The scope module decides which picture rows a wheel step shows and maps a
// click back to a frame and row. Those conversions are the part that must not
// drift across a gap or a frame seam, so this harness stubs just enough DOM to
// run the real module under Node and asserts the observable results. It is not
// part of the Go test suite; run it directly:
//
//     node examples/scope-zoom-check.mjs
import { strict as assert } from 'node:assert';

// The embedded modules read these globals at import or draw time.
globalThis.window = { devicePixelRatio: 1 };
globalThis.ResizeObserver = class {
  observe() {}
};
globalThis.document = {
  createElement: () => ({ setAttribute() {}, addEventListener() {}, append() {}, textContent: '' }),
};

const listeners = {};
const canvas = {
  clientWidth: 800,
  clientHeight: 200,
  getContext: () => ({
    setTransform() {}, fillRect() {}, beginPath() {}, moveTo() {}, lineTo() {},
    stroke() {}, setLineDash() {}, save() {}, restore() {}, rect() {}, clip() {},
    fillStyle: '',
  }),
  addEventListener: (type, fn) => { (listeners[type] ||= []).push(fn); },
  getBoundingClientRect: () => ({ left: 0, width: 800 }),
};
const controls = {
  '#waveform': canvas,
  '#scope-clock': { textContent: '' },
  '#scope-view': { textContent: '' },
  '#zoom-in': { addEventListener() {} },
  '#zoom-out': { addEventListener() {} },
  '#zoom-reset': { addEventListener() {}, disabled: false },
  '#coverage-list': { replaceChildren() {} },
};
const root = { querySelector: (selector) => controls[selector] };

const { scope } = await import('../internal/lightbench/assets/scripts/scope.js');

const coverage = [
  { picture_row_start: 2000, picture_row_end: 3770, status: 'readable' },
  { picture_row_start: 3770, picture_row_end: 5770, status: 'missing_sheet' },
  { picture_row_start: 5770, picture_row_end: 8000, status: 'readable' },
];
const data = {
  reel: 'cinder-a',
  params: { start: '1', end: '4', profile: 'bench' },
  window: { start: 1, end: 4 },
  rows_per_frame: 2000,
  rate: 48000,
  coverage,
  envelope: [{ start: 2000, end: 8000, readable: 3323, min: [0, 0], max: [1000, 1000], min_row: [0, 0], max_row: [0, 0] }],
};

// A fake station mirrors the binned reply: one bin per requested row, the gap
// marking rows 3770..5770 as unreadable with no extrema rows.
const replies = [];
globalThis.fetch = async (url, options = {}) => {
  const query = new URL(url, 'http://station').searchParams;
  const detail = { start: Number(query.get('start_row')), end: Number(query.get('end_row')) };
  replies.push(detail);
  const envelope = [];
  for (let row = detail.start; row < detail.end; row++) {
    const readable = row < 3770 || row >= 5770;
    envelope.push({
      start: row, end: row + 1, readable: readable ? 1 : 0,
      min: [0, 0], max: [readable ? 1000 : 0, readable ? 1000 : 0],
      min_row: [readable ? row : -1, readable ? row : -1],
      max_row: [readable ? row : -1, readable ? row : -1],
    });
  }
  await new Promise((resolve) => setTimeout(resolve, 0));
  if (options.signal?.aborted) throw Object.assign(new Error('aborted'), { name: 'AbortError' });
  return { ok: true, status: 200, json: async () => ({ ...data, detail, envelope }) };
};

const picks = [];
const waveform = scope(root, (frame, row) => picks.push([frame, row]));
waveform.setWindow(data);

const settle = () => new Promise((resolve) => setTimeout(resolve, 5));

// Clicking the middle of the whole window lands on the frame/row at that
// fraction, independent of any zoom.
listeners.click[0]({ clientX: 400 });
assert.deepEqual(picks.at(-1), [2, 1000], 'whole-window click maps to frame 2 row 1000');

// Rapid wheel steps must compound: forty steps at 1.25 with an 8-row floor go
// straight to the floor rather than each re-reading the previous reply.
const wheel = listeners.wheel[0];
for (let step = 0; step < 40; step++) {
  wheel({ ctrlKey: true, deltaY: -1, clientX: 400, preventDefault() {} });
}
await settle();
const zoomed = replies.at(-1);
assert.equal(zoomed.end - zoomed.start, 8, 'wheel steps compound down to the 8-row floor');

// A click inside the zoomed view maps back to an absolute row inside exactly
// that interval, so the probe reports the row the operator pointed at.
listeners.click[0]({ clientX: 400 });
const [frame, row] = picks.at(-1);
const absolute = frame * data.rows_per_frame + row;
assert.ok(absolute > zoomed.start && absolute < zoomed.end, 'zoomed click stays inside the view');

// Zooming while the prior reply is in flight must not paint bins under a
// different mapping; the newest request always wins.
for (let step = 0; step < 8; step++) {
  wheel({ ctrlKey: true, deltaY: -1, clientX: 0, preventDefault() {} });
}
await settle();
const finalReply = replies.at(-1);
assert.ok(finalReply.start >= 2000, 'zoom stays inside the applied window');
assert.ok(finalReply.end <= 8000, 'zoom stays inside the applied window');

// Zoom-out returns toward the whole window; double-click resets it exactly.
listeners.dblclick[0]();
await settle();
const reset = replies.at(-1);
assert.deepEqual(reset, { start: 2000, end: 8000 }, 'reset asks for the whole applied window');
assert.equal(controls['#scope-view'].textContent, '整窗', 'reset restores the whole-window badge');

console.log(JSON.stringify({
  whole_click: [2, 1000],
  zoom_floor_rows: zoomed.end - zoomed.start,
  zoomed_click_absolute: absolute,
  reset: reset,
  requests: replies.length,
}));
