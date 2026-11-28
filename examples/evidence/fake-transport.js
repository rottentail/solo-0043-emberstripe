// Deterministic in-memory transport for the probe evidence tests.
//
// It reproduces the cinder-a sample geometry (lead +230 rows, sheet 2 missing)
// without any network or DOM, and lets tests hold requests open so late
// responses can be delivered after the probe has moved on.

import { PICTURE, MAPPING, READING } from '../../internal/lightbench/assets/scripts/evidence.js';

export const ROWS = 2000;
export const FRAMES = 7;
export const LEAD = 230;
export const GAP = 2;
export const RATE = 48000;

export function windowResponse(profile = 'bench', start = 0, end = 6) {
  const frames = [];
  for (let frame = start; frame < end; frame += 1) {
    const depth = frame === GAP ? 0 : 16;
    frames.push({ frame, depth, readable: depth ? ROWS : 0, missing: 0, outside: 0 });
  }
  return {
    reel: 'cinder-a',
    window: { start, end },
    rate: RATE,
    rows_per_frame: ROWS,
    profile: { profile, lanes: [] },
    frames,
    coverage: [],
    envelope: [],
  };
}

function locate(frame, row) {
  const pictureRow = frame * ROWS + row;
  const source = pictureRow + LEAD;
  if (source < 0 || source >= ROWS * FRAMES) {
    return { picture_row: pictureRow, rate: RATE, lead_rows: LEAD, status: 'outside_capture' };
  }
  const address = { frame: Math.floor(source / ROWS), row: source % ROWS };
  const status = address.frame === GAP ? 'missing_sheet' : 'readable';
  return {
    picture_row: pictureRow,
    rate: RATE,
    lead_rows: LEAD,
    status,
    source: address,
  };
}

function reading(frame, row, profile) {
  const mapping = locate(frame, row);
  if (mapping.status !== 'readable') {
    return { status: mapping.status, gray: [0, 0], sample: [0, 0], clipped: [false, false] };
  }
  // Distinct, profile-tagged values so a stale response is unmistakable.
  const base = profile === 'bench' ? 1000 : 4000;
  const value = base + mapping.source.frame * 37 + mapping.source.row;
  return {
    status: 'readable',
    source: mapping.source,
    gray: [6000 + value, 8200 + value],
    sample: [value, -value],
    clipped: [false, false],
  };
}

// A deferred request the test can resolve or reject on demand.
export class PendingCall {
  constructor(kind, params) {
    this.kind = kind;
    this.params = params;
    this.promise = new Promise((resolve, reject) => {
      this.resolve = resolve;
      this.reject = reject;
    });
  }
}

export class FakeTransport {
  constructor() {
    this.calls = [];
    this.held = [];
    // fail[kind] makes the next matching call reject.
    this.fail = new Set();
    // latency[kind] auto-resolves after a microtask when not held.
    this.holdKinds = new Set();
  }

  record(kind, params, run) {
    const call = new PendingCall(kind, params);
    this.calls.push(call);
    if (this.fail.has(kind)) {
      this.fail.delete(kind);
      queueMicrotask(() => call.reject(new Error(`${kind} 暂时读不回来`)));
    } else if (this.holdKinds.has(kind)) {
      // Held calls stay pending even if the ledger aborts them, so a test can
      // resolve the old request later to reproduce a genuinely late answer.
      this.held.push(call);
    } else {
      queueMicrotask(() => run(call));
    }
    return call.promise;
  }

  loadPicture(params) {
    return this.record(PICTURE, params, (call) => call.resolve());
  }
  fetchMapping(params) {
    return this.record(MAPPING, params, (call) => call.resolve(locate(params.frame, params.row)));
  }
  fetchReading(params) {
    return this.record(READING, params, (call) => call.resolve(reading(params.frame, params.row, params.profile)));
  }

  release(kind) {
    const index = this.held.findIndex((call) => call.kind === kind);
    if (index < 0) throw new Error(`no held call of kind ${kind}`);
    const [call] = this.held.splice(index, 1);
    if (call.kind === PICTURE) call.resolve();
    else if (call.kind === MAPPING) call.resolve(locate(call.params.frame, call.params.row));
    else call.resolve(reading(call.params.frame, call.params.row, call.params.profile));
    return call;
  }

  reject(kind, message = '模拟的读取失败') {
    const index = this.held.findIndex((call) => call.kind === kind);
    if (index < 0) throw new Error(`no held call of kind ${kind}`);
    const [call] = this.held.splice(index, 1);
    call.reject(new Error(message));
    return call;
  }

  count(kind) {
    return this.calls.filter((call) => call.kind === kind).length;
  }
  last(kind) {
    return [...this.calls].reverse().find((call) => call.kind === kind);
  }
  flush() {
    return new Promise((resolve) => setTimeout(resolve, 0));
  }
}
