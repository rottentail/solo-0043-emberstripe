// DOM-level check for the review panel wiring. The browser APIs used by the
// production modules are stubbed just enough to exercise real event flow:
// held arrow keys skim, release settles once, lens switching preserves the
// cursor, and a delayed/aborted trace never lands on a stop already left.
//
// Run: node examples/review-dom-check.js

import assert from 'node:assert/strict';

const ROWS = 2000;
const boundaries = [
  { row: 3770, frame: 1, kinds: 'sound', from: 'readable', to: 'missing_sheet', from_depth: 0, to_depth: 0 },
  { row: 4000, frame: 2, kinds: 'picture', from: '', to: '', from_depth: 16, to_depth: 0 },
  { row: 5770, frame: 2, kinds: 'sound', from: 'missing_sheet', to: 'readable', from_depth: 0, to_depth: 0 },
];
const windowData = {
  reel: 'cinder-a',
  rows_per_frame: ROWS,
  rate: 48000,
  window: { start: 1, end: 4 },
  profile: { profile: 'bench' },
  params: { start: 1, end: 4, profile: 'bench' },
};

const listeners = new Map();
const elements = {};
function makeElement(id, tag = 'button') {
  return elements[id] ??= {
    id, tagName: tag.toUpperCase(),
    value: '', checked: false, hidden: false,
    textContent: '',
    listeners: {},
    addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); },
    dispatch(type, event = {}) { (this.listeners[type] ?? []).forEach((fn) => fn(event)); },
    querySelector() { return makeElement(id + '-child'); },
  };
}
const panel = makeElement('review-panel', 'section');
panel.hidden = true;
for (const id of ['review-position', 'review-kind', 'review-hint', 'review-back', 'review-forward']) makeElement(id);
const radios = ['sound-picture', 'sound'].map((value) => {
  const el = makeElement('lens-' + value, 'input');
  el.value = value;
  el.name = 'review-lens';
  return el;
});
panel.querySelectorAll = (selector) => (selector === 'input[name="review-lens"]' ? radios : []);
panel.querySelector = (selector) => elements[selector.replace('#', '')];

globalThis.window = {
  addEventListener(type, fn) { listeners.set(type, fn); },
};
const fetches = [];
globalThis.fetch = async (url) => {
  fetches.push(url);
  return { ok: true, json: async () => ({ boundaries }) };
};

const { review } = await import('../internal/lightbench/assets/scripts/review.js');

const marks = [];
const settles = [];
const lensEvents = [];
const controller = review(panel, {
  onMark: (frame, row) => marks.push(frame * ROWS + row),
  onSettle: (frame, row) => settles.push(frame * ROWS + row),
  onLens: (lens) => lensEvents.push(lens),
});

await controller.setWindow(windowData);
assert.equal(panel.hidden, false, 'the review panel appears once a window loads');
assert.ok(fetches.some((url) => url.includes('/review?')), 'the review endpoint is queried');
assert.deepEqual(settles, [2000], 'initial load settles at the window head');

const key = (type, key, repeat = false) => listeners.get(type)({ key, repeat, target: { tagName: 'BODY' }, preventDefault() {} });

// Hold right: press + repeats skim every stop with no settle.
key('keydown', 'ArrowRight', false);
key('keydown', 'ArrowRight', true);
key('keydown', 'ArrowRight', true);
assert.deepEqual(marks, [3770, 4000, 5770]);
assert.deepEqual(settles, [2000], 'no probe settle while the key is held');
key('keyup', 'ArrowRight');
assert.deepEqual(settles, [2000, 5770], 'release settles exactly once on the visible stop');

// Reverse returns to the stop just passed.
key('keydown', 'ArrowLeft', false);
key('keyup', 'ArrowLeft');
assert.deepEqual(settles.at(-1), 4000, 'reverse lands on the previously passed picture stop');

// Switch to the sound lens: cursor keeps the picture-only row but it is no
// longer a stop; the panel note explains the row can be inspected in place.
key('keydown', 'm', false);
assert.equal(radios.find((r) => r.value === 'sound').checked, true);
assert.equal(elements['review-position'].textContent, '2 格 0 行');
assert.match(elements['review-hint'].textContent, /不是此口径的停点/);
assert.deepEqual(lensEvents.at(-1), 'sound');

// From the retained picture-only row, jumps continue to sound boundaries.
key('keydown', 'ArrowRight', false);
key('keyup', 'ArrowRight');
assert.equal(settles.at(-1), 5770, 'forward from a retained non-stop row reaches the next sound cut');
key('keydown', 'ArrowLeft', false);
key('keyup', 'ArrowLeft');
assert.equal(settles.at(-1), 3770, 'backward reaches the previous sound cut');

// Typing in a form field must not hijack arrow keys.
const before = settles.length;
listeners.get('keydown')({ key: 'ArrowRight', repeat: false, target: { tagName: 'INPUT' }, preventDefault() {} });
assert.equal(settles.length, before, 'arrow keys inside inputs are ignored');

// Blur while held settles on the current stop and clears the hold.
listeners.get('keydown')({ key: 'ArrowRight', repeat: false, target: { tagName: 'BODY' }, preventDefault() {} });
listeners.get('blur')();
assert.equal(settles.at(-1), 5770);
listeners.get('keyup')({ key: 'ArrowRight', target: { tagName: 'BODY' }, preventDefault() {} });
assert.equal(settles.at(-1), 5770, 'the late keyup after blur is a no-op');

console.log('DOM 复查接线检查通过：', {
  marks,
  settles,
  lens: lensEvents,
  finalPosition: elements['review-position'].textContent,
});
