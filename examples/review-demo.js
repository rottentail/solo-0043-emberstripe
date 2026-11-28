// Console demonstration of the browser review engine and held-key
// controller. It imports the exact production modules used by the
// workbench (no copies) and plays back the restorer's keyboard session:
//
//   1. a delayed-returning probe whose stale answers must never overwrite
//      the stop shown after a held skim;
//   2. continuous reverse jumps that return to the stops just passed;
//   3. switching between the sound and sound-picture lenses, including a
//      picture-only stop where the sound lens can still inspect in place
//      and continue toward sound boundaries on either side.
//
// Run: node examples/review-demo.js

import assert from 'node:assert/strict';
import {
  createReviewController,
  createReviewEngine,
  PICTURE_KIND,
  SOUND_KIND,
  SOUND_LENS,
  SOUND_PICTURE_LENS,
  hasKind,
} from '../internal/lightbench/assets/scripts/review-engine.js';

// Fixtures mirror the /lightbench/{reel}/review JSON shape (snake_case as
// emitted by Go). Rows are absolute picture rows at 2000 rows per frame.
const ROWS = 2000;
const windowA = {
  start: 2000,
  rows: ROWS,
  boundaries: [
    { row: 3770, frame: 1, kinds: 'sound', from: 'readable', to: 'missing_sheet', from_depth: 0, to_depth: 0 },
    { row: 4000, frame: 2, kinds: 'picture', from: '', to: '', from_depth: 16, to_depth: 0 },
    { row: 5770, frame: 2, kinds: 'sound', from: 'missing_sheet', to: 'readable', from_depth: 0, to_depth: 0 },
    { row: 6000, frame: 3, kinds: 'picture', from: '', to: '', from_depth: 0, to_depth: 16 },
  ],
};
// A second window where a picture cut and a sound cut sit on the same row.
const windowB = {
  start: 8000,
  rows: ROWS,
  boundaries: [
    { row: 8300, frame: 4, kinds: 'sound', from: 'readable', to: 'outside_capture', from_depth: 0, to_depth: 0 },
    { row: 10000, frame: 5, kinds: 'sound,picture', from: 'outside_capture', to: 'readable', from_depth: 8, to_depth: 16 },
    { row: 11000, frame: 5, kinds: 'picture', from: '', to: '', from_depth: 16, to_depth: 0 },
  ],
};

const log = [];
function record(event, detail) {
  const line = `${event.padEnd(22)} ${detail}`;
  log.push(line);
  console.log(line);
}

// ---- Delayed probe -------------------------------------------------------
// The browser trace probe answers out of order. An abort token marks the
// request that was superseded by newer navigation, exactly like the
// AbortController used by probe.js.
function delayedProbe() {
  const pending = new Map();
  let serial = 0;
  return {
    ask(row, delay) {
      const token = ++serial;
      pending.set(token, row);
      return new Promise((resolve) => {
        setTimeout(() => {
          if (!pending.has(token)) {
            record('stale probe dropped', `row ${row} answer arrived after abort`);
            resolve(null);
            return;
          }
          pending.delete(token);
          resolve({ token, row, sample: [row % 32767, -(row % 1000)] });
        }, delay);
      });
    },
    abortAll() {
      pending.clear();
    },
  };
}

// ---- Session 1: held forward skim, release, delayed evidence -------------
function heldSkimAndSettle() {
  const engine = createReviewEngine({ boundaries: windowA.boundaries, lens: SOUND_PICTURE_LENS, row: windowA.start });
  const probe = delayedProbe();
  const settled = [];
  const marks = [];
  const controller = createReviewController({
    engine,
    onMark: (row) => marks.push(row),
    onSettle: (row) => settled.push(row),
  });

  // Hold the right key: one press plus browser key-repeat events skim every
  // stop in the window without any settle/probe request.
  controller.press(1, false);
  controller.press(1, true);
  controller.press(1, true);
  controller.press(1, true);
  assert.deepEqual(marks, [3770, 4000, 5770, 6000], 'held key must skim every stop in order');
  assert.deepEqual(settled, [], 'no settle while the jump key is held');
  assert.equal(engine.row, 6000, 'cursor rests on the last stop passed');

  // A probe for an earlier stop is still in flight when the operator lets go.
  const slow = probe.ask(3770, 60);
  probe.abortAll();
  controller.release(1);
  assert.deepEqual(settled, [6000], 'release settles exactly once, on the visible stop');

  const evidence = probe.ask(engine.row, 10);
  return evidence.then((reading) => {
    assert.ok(reading, 'the post-release probe must answer');
    assert.equal(reading.row, engine.row, 'final reading belongs to the settled stop');
    assert.equal(engine.row, 6000);
    return slow.then((stale) => {
      assert.equal(stale, null, 'the in-flight probe from the skimmed stop is discarded');
      record('delayed probe', `settle row 6000 -> reading ${JSON.stringify(reading.sample)}; stale row 3770 dropped`);
    });
  });
}

// ---- Session 2: continuous reverse returns to stops just passed ----------
function continuousReverse() {
  const engine = createReviewEngine({ boundaries: windowA.boundaries, lens: SOUND_PICTURE_LENS, row: 6000 });
  const trail = [];
  const controller = createReviewController({ engine, onMark: (row) => trail.push(row), onSettle: (row) => trail.push(`settle:${row}`) });

  // Tap left repeatedly; each move lands on the previously passed stop.
  for (let i = 0; i < 4; i += 1) {
    controller.press(-1, false);
    controller.release(-1);
  }
  assert.deepEqual(trail, [5770, 'settle:5770', 4000, 'settle:4000', 3770, 'settle:3770', 'settle:3770'],
    'reverse walks back stop by stop and clamps at the first stop');
  assert.equal(engine.row, 3770, 'the window head is never crossed');

  // A held reverse skim covers the same ground with one release settle.
  engine.setRow(6000);
  trail.length = 0;
  controller.press(-1, false);
  controller.press(-1, true);
  controller.press(-1, true);
  controller.release(-1);
  assert.deepEqual(trail, [5770, 4000, 3770, 'settle:3770'], 'held reverse replays the same stops, one settle');
  record('continuous reverse', `6000 -> ${trail.join(' -> ')}`);

  // Blur while held behaves like releasing on the current stop.
  engine.setRow(6000);
  controller.press(-1, false);
  controller.press(-1, true);
  assert.equal(controller.blur(), true);
  assert.equal(engine.row, 4000, 'blur settles on the stop visible at blur time');
  assert.equal(controller.holding, false);
  assert.equal(controller.blur(), false, 'a second blur without a hold is a no-op');
}

// ---- Session 3: lens switching preserves the confirmed cursor -------------
function lensSwitching() {
  const engine = createReviewEngine({ boundaries: windowA.boundaries, lens: SOUND_PICTURE_LENS, row: windowA.start });
  const expectedSoundRows = windowA.boundaries.filter((b) => hasKind(b, SOUND_KIND)).map((b) => b.row);
  const expectedAllRows = windowA.boundaries.map((b) => b.row);

  assert.deepEqual(engine.stops(), expectedAllRows, 'sound-picture lens includes picture cuts');
  engine.setLens(SOUND_LENS);
  assert.deepEqual(engine.stops(), expectedSoundRows, 'sound lens keeps only coverage cuts');
  assert.equal(engine.row, windowA.start, 'switching lens does not move the cursor');

  // Travel forward in the combined lens and park on the picture-only cut.
  engine.setLens(SOUND_PICTURE_LENS);
  engine.move(1); // 3770 sound
  engine.move(1); // 4000 picture only
  assert.equal(engine.row, 4000);
  assert.equal(engine.atStop(), true);

  // Switch back to the sound lens: the picture-only row is retained, but is
  // no longer a stop. Inspecting in place stays on it; jumps leave from there.
  engine.setLens(SOUND_LENS);
  assert.equal(engine.row, 4000, 'confirmed position survives the lens switch');
  assert.equal(engine.atStop(), false, 'picture-only row is not a sound-lens stop');
  assert.equal(engine.move(1), 5770, 'forward continues from the retained row to the next sound cut');
  engine.setRow(4000);
  assert.equal(engine.move(-1), 3770, 'backward reaches the previous sound cut');
  engine.setRow(4000);
  engine.setLens(SOUND_PICTURE_LENS);
  assert.equal(engine.atStop(), true, 'the same row is a stop again under the combined lens');
  record('lens switch', 'picture-only row 4000 retained; sound jumps reach 3770 / 5770');
}

// ---- Session 4: same-row sound+picture cut and the second window ----------
function sameRowAndWindowChange() {
  const engine = createReviewEngine({ boundaries: windowA.boundaries, lens: SOUND_PICTURE_LENS, row: windowA.start });
  // The reload keeps the operator's chosen lens but re-anchors the cursor at
  // the new window head, mirroring review.setWindow.
  const lens = engine.lens;
  const next = createReviewEngine({ boundaries: windowB.boundaries, lens, row: windowB.start });
  assert.equal(next.lens, SOUND_PICTURE_LENS);
  assert.equal(next.row, 8000);

  next.move(1);
  next.move(1);
  const shared = next.boundary();
  assert.equal(next.row, 10000);
  assert.ok(hasKind(shared, SOUND_KIND) && hasKind(shared, PICTURE_KIND), 'one stop can carry both boundary kinds');
  assert.equal(next.atStop(), true);

  next.setLens(SOUND_LENS);
  assert.equal(next.row, 10000, 'cursor stays on the shared row after the lens switch');
  assert.equal(next.atStop(), true, 'a shared row remains a stop in the sound lens');
  assert.deepEqual(next.stops(), [8300, 10000], 'the picture-only cut at 11000 disappears under the sound lens');
  next.move(1);
  assert.equal(next.row, 10000, 'no further sound stop beyond the shared row');
  record('same-row boundary', 'row 10000 is one stop for both lenses; picture-only 11000 hidden when listening only');
}

// ---- Final readout consistency -------------------------------------------
function finalReadout() {
  const engine = createReviewEngine({ boundaries: windowA.boundaries, lens: SOUND_PICTURE_LENS, row: windowA.start });
  const controller = createReviewController({ engine, onMark() {}, onSettle() {} });
  const expected = [3770, 4000, 5770, 6000, 5770, 4000, 3770];
  const observed = [];
  for (let i = 0; i < 4; i += 1) {
    controller.press(1, i > 0);
    observed.push(engine.row);
  }
  controller.release(1);
  for (let i = 0; i < 3; i += 1) {
    controller.press(-1, false);
    observed.push(engine.row);
    controller.release(-1);
  }
  assert.deepEqual(observed, expected, 'forward then reverse reproduces the same stop readings');
  assert.equal(engine.row, 3770);
  record('final readout', `stop sequence ${observed.join(' -> ')} matches the boundary table`);
}

const runs = [heldSkimAndSettle(), Promise.resolve(continuousReverse()), Promise.resolve(lensSwitching()),
  Promise.resolve(sameRowAndWindowChange()), Promise.resolve(finalReadout())];
Promise.all(runs)
  .then(() => {
    console.log('\n全部演示通过：');
    for (const line of log) console.log('  ' + line);
  })
  .catch((error) => {
    console.error('演示失败：', error);
    process.exitCode = 1;
  });
