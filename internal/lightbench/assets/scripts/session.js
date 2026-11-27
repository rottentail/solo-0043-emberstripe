import { benchURL, request } from './client.js';

const nearestRow = (absolute, start, end) => Math.min(Math.max(absolute, start), end - 1);

// planWindowCarry decides which absolute picture rows survive a window change.
// The picture clock does not depend on the window, so the probe selection and
// the actual playback landing point can both be carried when the old and new
// windows overlap. A point outside the new window, or a landing point measured
// under another calibration, is reported instead of silently jumping, together
// with the closest visible row the workbench can relocate to.
export function planWindowCarry(previous, next, snapshot = {}) {
  const rows = next.rows_per_frame;
  const start = next.window.start * rows;
  const end = next.window.end * rows;
  const point = (absolute) => {
    if (absolute == null || !Number.isFinite(absolute)) return null;
    return { absolute, frame: Math.floor(absolute / rows), row: Math.floor(absolute) % rows };
  };
  const inside = (absolute) => Number.isFinite(absolute) && absolute >= start && absolute < end;
  const plan = {
    previous: previous?.window ?? null,
    overlap: false,
    profileChanged: false,
    audio: Boolean(snapshot.audio),
    selection: null,
    playhead: null,
    lostSelection: null,
    lostPlayhead: null,
    nearestSelection: null,
    nearestPlayhead: null,
    resume: false,
  };
  if (!previous) return plan;

  const selection = snapshot.selection;
  const playhead = snapshot.playhead;
  plan.overlap = previous.window.start < next.window.end && next.window.start < previous.window.end;
  plan.profileChanged = previous.profile.profile !== next.profile.profile;

  if (plan.overlap && inside(selection)) {
    plan.selection = point(selection);
  } else if (point(selection)) {
    plan.lostSelection = point(selection);
    plan.nearestSelection = point(nearestRow(selection, start, end));
  }
  // A landing point is bound to the calibration that produced its PCM, so a
  // calibration change always asks the operator to relocate explicitly.
  if (plan.overlap && !plan.profileChanged && inside(playhead)) {
    plan.playhead = point(playhead);
    plan.resume = Boolean(snapshot.playing);
  } else if (point(playhead)) {
    plan.lostPlayhead = point(playhead);
    plan.nearestPlayhead = point(nearestRow(playhead, start, end));
  }
  return plan;
}

// carrySummary names, in the window status line, whether the previous probe and
// playback points came along or where the operator can pick them up again.
export function carrySummary(plan) {
  if (!plan?.previous) return '';
  if (plan.lostSelection || plan.lostPlayhead) {
    return plan.profileChanged
      ? ' 标定已更改，原画格行或播放落点需重新定位。'
      : ' 原画格行或播放落点不在新窗口，见探针与试听提示。';
  }
  if (plan.selection || plan.playhead) return ' 已保留原画格行。';
  return '';
}

export function windowController(root, onWindow, snapshot) {
  const reel = root.dataset.reel;
  const form = root.querySelector('#window-form');
  const message = root.querySelector('#window-status');
  let current = null;
  let inflight = null;
  let queued = null;
  let busy = false;
  let latest = 0;

  const params = () => Object.fromEntries(new FormData(form));

  async function apply(input, generation) {
    const controller = new AbortController();
    inflight = controller;
    delete message.dataset.error;
    message.textContent = '正在读取声槽与扫描页…';
    try {
      const data = await request(benchURL(reel, 'window', input), { signal: controller.signal });
      // A newer submission invalidates this response even if the abort landed
      // too late, so only the newest applied window can reach the panels.
      if (generation !== latest) return;
      // Read the live probe and playback point when the response is ready, so
      // an operator move during the request is what gets carried.
      const carry = planWindowCarry(current, data, snapshot());
      current = { ...data, reel, params: input };
      message.textContent = `当前窗口 ${data.window.start}–${data.window.end - 1} 格 · 标定 ${data.profile.profile}${carrySummary(carry)}`;
      onWindow(current, carry);
    } catch (error) {
      if (generation !== latest || controller.signal.aborted) return;
      const previous = current ? ` 仍在查看 ${current.window.start}–${current.window.end - 1} 格，标定 ${current.profile.profile}。` : '';
      message.dataset.error = '';
      message.textContent = error.message + previous;
    } finally {
      if (inflight === controller) inflight = null;
    }
  }

  // At most one window request runs at a time. A newer submission makes every
  // older response stale and is applied once on its own, so the waveform,
  // probe and audition all end on that same successfully applied window
  // instead of on whichever response happened to arrive last.
  async function drain(input, generation) {
    busy = true;
    let next = { input, generation };
    while (next) {
      await apply(next.input, next.generation);
      next = queued;
      queued = null;
    }
    busy = false;
  }

  function update(event) {
    event?.preventDefault();
    latest++;
    const input = params();
    if (busy) {
      queued = { input, generation: latest };
      inflight?.abort();
      message.textContent = '正在应用最新区间，稍后自动继续…';
      return;
    }
    void drain(input, latest);
  }

  form.addEventListener('submit', update);
  return { start: update };
}
