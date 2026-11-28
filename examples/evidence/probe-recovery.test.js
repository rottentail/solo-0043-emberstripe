// Evidence-based probe recovery tests.
//
// Run with: node --test examples/evidence/probe-recovery.test.js
//
// These tests exercise the same evidence ledger the browser uses, with a
// deterministic in-memory transport standing in for the HTTP station. The two
// scenarios from the work order are covered explicitly:
//
//   A. two calibrations on the same picture frame, including a late old-profile
//      answer that must never be paired with the new calibration;
//   B. one positioning across the missing sheet (frame 2 has no picture page)
//      where the sound slot comes from another offset page and remains audible.

import test from 'node:test';
import assert from 'node:assert/strict';

import {
  ABSENT, FAILED, PENDING, PICTURE, MAPPING, READING, STALE, VERIFIED,
  createProbeLedger,
} from '../../internal/lightbench/assets/scripts/evidence.js';
import { FakeTransport, GAP, windowResponse } from './fake-transport.js';

function setup(profile = 'bench', range = [0, 6]) {
  const transport = new FakeTransport();
  const ledger = createProbeLedger(transport);
  ledger.setWindow(windowResponse(profile, range[0], range[1]));
  return { transport, ledger };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

// Wait until every watched piece leaves the pending state. STALE is a settled
// display state (retained evidence awaiting an explicit or automatic refetch).
async function settled(ledger, { pieces = [PICTURE, MAPPING, READING], timeout = 500 } = {}) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const states = pieces.map((id) => ledger.piece(id).state);
    if (states.every((state) => state !== PENDING && state !== null)) return;
    await tick();
  }
  throw new Error(`ledger did not settle: ${JSON.stringify(ledger.pieces())}`);
}

test('initial probe verifies three independent pieces for a readable frame', async () => {
  const { transport, ledger } = setup();
  ledger.select(0, 400);

  assert.equal(ledger.piece(PICTURE).state, PENDING);
  assert.equal(ledger.piece(MAPPING).state, PENDING);
  await settled(ledger);

  const pieces = ledger.pieces();
  assert.equal(pieces.picture.state, VERIFIED);
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(pieces.reading.state, VERIFIED);
  assert.equal(pieces.mapping.data.status, 'readable');
  assert.equal(pieces.mapping.data.source.frame, 0);
  assert.equal(pieces.reading.data.sample[0], 1000 + 630);
  assert.equal(transport.count(READING), 1);
});

test('a missing picture page is recorded as absent material while the offset sound stays readable', async () => {
  const { ledger } = setup();
  // Frame GAP has no picture sheet; row 1800 maps onto sound page 3, which exists.
  ledger.select(GAP, 1800);
  await settled(ledger);

  const pieces = ledger.pieces();
  assert.equal(pieces.picture.state, ABSENT);
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(pieces.reading.state, VERIFIED);
  assert.equal(pieces.mapping.data.status, 'readable');
  assert.equal(pieces.mapping.data.source.frame, GAP + 1);
  assert.ok(pieces.reading.data.sample[0] > 0);
});

test('a sound slot moved outside the capture is absent material, not a retryable failure', async () => {
  const { transport, ledger } = setup();
  // Reproduce a negative-lead / outside-capture mapping directly through the
  // same transport shape the station returns for vireo-b frame 0 early rows.
  transport.fetchMapping = ({ signal }) => Promise.resolve({
    picture_row: 0, rate: 48000, lead_rows: -135, status: 'outside_capture',
  });
  ledger.select(0, 0);
  await settled(ledger, { pieces: [PICTURE, MAPPING] });

  const pieces = ledger.pieces();
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(pieces.mapping.data.status, 'outside_capture');
  assert.equal(pieces.reading.state, ABSENT);
  assert.equal(pieces.reading.data.status, 'outside_capture');
  assert.equal(transport.count(READING), 0);

  ledger.retry();
  await tick();
  assert.equal(transport.count(READING), 0);
});

test('a sound slot on the missing sheet is reported as absent material, not as a failure to retry', async () => {
  const { transport, ledger } = setup();
  // Frame 1 row 1770 maps to source frame 2 row 0, which has no sheet.
  ledger.select(1, 1770);
  await settled(ledger);

  const pieces = ledger.pieces();
  assert.equal(pieces.picture.state, VERIFIED);
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(pieces.mapping.data.status, 'missing_sheet');
  assert.equal(pieces.reading.state, ABSENT);
  assert.equal(pieces.reading.data.status, 'missing_sheet');

  // Absent material must not generate or accept a reading request.
  const readingCalls = transport.count(READING);
  ledger.retry();
  await tick();
  assert.equal(transport.count(READING), readingCalls);
});

test('partial failure keeps verified evidence and retries only the failed piece', async () => {
  const { transport, ledger } = setup();
  transport.fail.add(PICTURE);
  ledger.select(0, 400);
  await settled(ledger);

  let pieces = ledger.pieces();
  assert.equal(pieces.picture.state, FAILED);
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(pieces.reading.state, VERIFIED);

  const readingCallsBefore = transport.count(READING);
  const mappingCallsBefore = transport.count(MAPPING);
  ledger.retry([PICTURE]);
  await settled(ledger);

  pieces = ledger.pieces();
  assert.equal(pieces.picture.state, VERIFIED);
  // The still-valid pieces are not re-requested.
  assert.equal(transport.count(READING), readingCallsBefore);
  assert.equal(transport.count(MAPPING), mappingCallsBefore);
});

test('retry-all skips verified and absent pieces and only refetches what is still missing', async () => {
  const { transport, ledger } = setup();
  ledger.select(GAP, 1800);
  await settled(ledger);
  transport.fail.add(MAPPING);

  // Move to a fresh position, then make its mapping fail.
  ledger.select(4, 10);
  await settled(ledger);
  assert.equal(ledger.piece(MAPPING).state, FAILED);
  assert.equal(ledger.piece(PICTURE).state, VERIFIED);

  const callsBefore = {
    picture: transport.count(PICTURE),
    mapping: transport.count(MAPPING),
  };
  ledger.retry();
  await settled(ledger);

  assert.equal(ledger.piece(MAPPING).state, VERIFIED);
  assert.equal(transport.count(PICTURE), callsBefore.picture);
  assert.equal(transport.count(MAPPING), callsBefore.mapping + 1);
});

test('scenario A: switching calibration keeps mapping and picture, marks only the old reading stale, and refetches it', async () => {
  const { transport, ledger } = setup('bench');
  ledger.select(0, 400);
  await settled(ledger);
  const benchSample = ledger.piece(READING).data.sample[0];

  // Apply the same window with the other calibration.
  ledger.setWindow(windowResponse('soft', 0, 6));
  ledger.select(0, 400);

  let pieces = ledger.pieces();
  assert.equal(pieces.picture.state, VERIFIED);
  assert.equal(pieces.mapping.state, VERIFIED);
  // The old bench reading can no longer be shown as current. It is retired and
  // immediately re-requested against soft; it must never remain "verified".
  assert.ok([STALE, PENDING].includes(pieces.reading.state));
  assert.notEqual(pieces.reading.state, VERIFIED);
  // No new picture or mapping request is needed for the same position.
  assert.equal(transport.count(PICTURE), 1);
  assert.equal(transport.count(MAPPING), 1);

  await settled(ledger);
  pieces = ledger.pieces();
  assert.equal(pieces.reading.state, VERIFIED);
  assert.notEqual(pieces.reading.data.sample[0], benchSample);
  assert.equal(pieces.reading.data.sample[0], 4000 + 630);
  assert.equal(transport.count(READING), 2);
});

test('scenario A: a late old-calibration answer is rejected and never paired with the new calibration', async () => {
  const { transport, ledger } = setup('bench');
  transport.holdKinds.add(READING);

  ledger.select(0, 400);
  await tick();
  const oldCall = transport.held.find((call) => call.kind === READING);
  assert.ok(oldCall);
  assert.equal(oldCall.params.profile, 'bench');

  // Before the bench answer returns, switch to soft. The stale request is
  // aborted and a soft-keyed reading is requested.
  ledger.setWindow(windowResponse('soft', 0, 6));
  ledger.select(0, 400);
  assert.equal(ledger.piece(READING).state, PENDING);
  transport.holdKinds.delete(READING);
  await tick();

  const softCall = transport.last(READING);
  assert.equal(softCall.params.profile, 'soft');

  // The old bench response finally arrives late. Its key no longer matches, so
  // it must not overwrite the current (still pending) reading.
  oldCall.resolve({
    status: 'readable',
    source: { frame: 0, row: 630 },
    gray: [6000, 8200],
    sample: [99999, -99999],
    clipped: [true, true],
  });
  await tick();
  assert.equal(ledger.piece(READING).state, PENDING);

  softCall.resolve({
    status: 'readable',
    source: { frame: 0, row: 630 },
    gray: [12000, 14000],
    sample: [4630, -4630],
    clipped: [false, false],
  });
  await settled(ledger);

  const reading = ledger.piece(READING);
  assert.equal(reading.state, VERIFIED);
  assert.equal(reading.data.sample[0], 4630);
  assert.deepEqual(reading.data.clipped, [false, false]);
  assert.notEqual(reading.data.sample[0], 99999);
});

test('scenario B: cross-gap positioning distinguishes absent picture from readable offset sound, even with a late picture', async () => {
  const { transport, ledger } = setup();
  transport.holdKinds.add(PICTURE);

  // First verify an ordinary frame whose picture is available.
  ledger.select(0, 400);
  const firstPicture = transport.held.find((call) => call.kind === PICTURE);
  assert.ok(firstPicture);
  firstPicture.resolve();
  await settled(ledger);
  assert.equal(ledger.piece(PICTURE).state, VERIFIED);

  // Jump to the gap frame. Its picture is material-absent immediately from the
  // window data; mapping and reading proceed from the offset page.
  ledger.select(GAP, 1800);
  assert.equal(ledger.piece(PICTURE).state, ABSENT);
  assert.equal(ledger.piece(MAPPING).state, PENDING);
  await settled(ledger, { pieces: [MAPPING, READING] });

  assert.equal(ledger.piece(MAPPING).state, VERIFIED);
  assert.equal(ledger.piece(READING).state, VERIFIED);
  assert.equal(ledger.piece(READING).data.source.frame, GAP + 1);
  assert.equal(ledger.piece(PICTURE).state, ABSENT);
});

test('moving to another frame rejects the late answer of the previous frame', async () => {
  const { transport, ledger } = setup();
  transport.holdKinds.add(MAPPING);

  ledger.select(0, 400);
  const firstMapping = transport.held.find((call) => call.kind === MAPPING);
  assert.equal(firstMapping.params.frame, 0);
  assert.equal(firstMapping.params.row, 400);

  ledger.select(3, 10);
  transport.holdKinds.delete(MAPPING);
  await tick();
  const current = transport.last(MAPPING);
  assert.equal(current.params.frame, 3);

  // Resolve the stale first-frame answer late, then the current one.
  firstMapping.resolve({ status: 'readable', source: { frame: 0, row: 630 } });
  await tick();
  assert.notDeepEqual(ledger.piece(MAPPING).data?.source, { frame: 0, row: 630 });

  current.resolve({ status: 'readable', source: { frame: 3, row: 240 } });
  await settled(ledger, { pieces: [MAPPING, READING] });

  const mapping = ledger.piece(MAPPING);
  assert.equal(mapping.state, VERIFIED);
  assert.deepEqual(mapping.data.source, { frame: 3, row: 240 });
});

test('reading failure does not invalidate the verified mapping and a single-piece retry recovers it', async () => {
  const { transport, ledger } = setup();
  transport.fail.add(READING);
  ledger.select(0, 400);
  await settled(ledger);

  let pieces = ledger.pieces();
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(pieces.reading.state, FAILED);

  ledger.retry([READING]);
  await settled(ledger);
  pieces = ledger.pieces();
  assert.equal(pieces.reading.state, VERIFIED);
  assert.equal(pieces.mapping.state, VERIFIED);
  assert.equal(transport.count(MAPPING), 1);
});

test('abort marks in-flight pieces failed and a targeted retry recovers them', async () => {
  const { transport, ledger } = setup();
  transport.holdKinds.add(READING);
  ledger.select(0, 400);
  await tick();
  assert.equal(ledger.piece(READING).state, PENDING);

  ledger.abort();
  assert.equal(ledger.piece(READING).state, FAILED);

  transport.holdKinds.delete(READING);
  ledger.retry([READING]);
  await settled(ledger);
  assert.equal(ledger.piece(READING).state, VERIFIED);
});
