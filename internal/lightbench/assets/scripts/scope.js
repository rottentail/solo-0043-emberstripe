import { compactNumber, position, statusName } from './client.js';
import { context, fraction, vertical } from './canvas.js';

const fills = { readable: '#29392f', missing_sheet: '#58352e', outside_capture: '#484741' };
const gapTint = { missing_sheet: 'rgba(190, 113, 102, 0.18)', outside_capture: 'rgba(147, 145, 141, 0.14)' };
const rangeTint = 'rgba(255, 209, 102, 0.13)';
const handleColor = '#ffd166';
const grabPx = 7;
const clickSlop = 4;

// The scope draws the applied window on the picture-row clock. The re-listen
// range shares that clock: endpoints snap to whole rows, gaps keep their
// duration, and the switchable ruler reads frame rows or source pages.
export function scope(root, onPosition) {
  const canvas = root.querySelector('#waveform');
  const rulerCanvas = root.querySelector('#ruler');
  const clock = root.querySelector('#scope-clock');
  const list = root.querySelector('#coverage-list');
  const form = root.querySelector('#range-form');
  const reset = root.querySelector('#range-reset');
  const rangeStatus = root.querySelector('#range-status');
  const toggles = [...root.querySelectorAll('[data-ruler]')];
  let data, cursor, playhead, drag;
  let mode = 'frames';
  let range = { start: 0, end: 0 };
  const listeners = [];

  const first = () => data.window.start * data.rows_per_frame;
  const last = () => data.window.end * data.rows_per_frame;
  const length = () => last() - first();
  const lead = () => data.lead_rows ?? 0;

  function locate(absolute) {
    onPosition(Math.floor(absolute / data.rows_per_frame), absolute % data.rows_per_frame);
  }

  function runAt(absolute) {
    for (const run of data.coverage) if (absolute < run.picture_row_end) return run;
    return data.coverage[data.coverage.length - 1];
  }

  function frameReadout(absolute) {
    return `${Math.floor(absolute / data.rows_per_frame)} 格 ${absolute % data.rows_per_frame} 行`;
  }

  // The sound ruler reports real page readings only where a sheet exists.
  function soundReadout(absolute) {
    const run = runAt(absolute);
    if (run.status === 'outside_capture') return '卷外 · 无源页';
    const source = absolute + lead();
    const page = Math.floor(source / data.rows_per_frame);
    if (run.status === 'missing_sheet') return `第 ${page} 页缺页`;
    return `第 ${page} 页 ${source % data.rows_per_frame} 行`;
  }

  const readout = (absolute) => mode === 'frames' ? frameReadout(absolute) : soundReadout(absolute);

  function readableRows(start, end) {
    let count = 0;
    for (const run of data.coverage) {
      if (run.status === 'readable') {
        count += Math.max(0, Math.min(run.picture_row_end, end) - Math.max(run.picture_row_start, start));
      }
    }
    return count;
  }

  function describeRange() {
    delete rangeStatus.dataset.error;
    const rows = range.end - range.start;
    rangeStatus.textContent = `起点 ${readout(range.start)} · 终点 ${readout(range.end)}（不含）· 共 ${compactNumber(rows)} 行 · ${compactNumber(rows * 1000 / data.rate)} 毫秒 · 可读 ${compactNumber(readableRows(range.start, range.end))} 行`;
  }

  function rangeError(message) {
    rangeStatus.dataset.error = '';
    rangeStatus.textContent = message;
  }

  function fillEndpoint(prefix, absolute) {
    const a = form.elements[`${prefix}-a`];
    const b = form.elements[`${prefix}-b`];
    if (mode === 'frames') {
      a.value = Math.floor(absolute / data.rows_per_frame);
      b.value = absolute % data.rows_per_frame;
      a.placeholder = b.placeholder = '';
      return;
    }
    const run = runAt(absolute);
    if (run.status === 'readable') {
      const source = absolute + lead();
      a.value = Math.floor(source / data.rows_per_frame);
      b.value = source % data.rows_per_frame;
      a.placeholder = b.placeholder = '';
    } else {
      a.value = b.value = '';
      a.placeholder = b.placeholder = run.status === 'missing_sheet' ? '缺页' : '卷外';
    }
  }

  const axisText = { frames: ['画格', '格内行'], sound: ['声槽页', '页内行'] };

  function applyMode() {
    for (const button of toggles) button.setAttribute('aria-pressed', String(button.dataset.ruler === mode));
    const [axisA, axisB] = axisText[mode];
    for (const span of form.querySelectorAll('[data-axis="a"]')) span.textContent = axisA;
    for (const span of form.querySelectorAll('[data-axis="b"]')) span.textContent = axisB;
    if (!data) return;
    const rows = data.rows_per_frame;
    const bounds = mode === 'frames'
      ? [data.window.start, data.window.end]
      : [Math.floor((first() + lead()) / rows), Math.floor((last() + lead()) / rows)];
    for (const prefix of ['start', 'end']) {
      const a = form.elements[`${prefix}-a`];
      const b = form.elements[`${prefix}-b`];
      a.min = bounds[0];
      a.max = bounds[1];
      b.min = 0;
      b.max = rows - 1;
    }
    fillEndpoint('start', range.start);
    fillEndpoint('end', range.end);
    describeRange();
    drawRuler();
  }

  function commit() {
    fillEndpoint('start', range.start);
    fillEndpoint('end', range.end);
    describeRange();
    draw();
    drawRuler();
    for (const listener of listeners) listener(range.start, range.end);
  }

  function tooltip(paint, width, tx, text) {
    paint.font = '11px system-ui, sans-serif';
    paint.textBaseline = 'middle';
    const w = paint.measureText(text).width + 12;
    const bx = Math.min(Math.max(tx - w / 2, 2), width - w - 2);
    paint.fillStyle = 'rgba(16, 18, 17, 0.94)';
    paint.strokeStyle = handleColor;
    paint.fillRect(bx, 4, w, 18);
    paint.strokeRect(bx, 4, w, 18);
    paint.fillStyle = '#fbf5e6';
    paint.fillText(text, bx + 6, 13);
    paint.textBaseline = 'alphabetic';
  }

  function draw() {
    const { paint, width, height } = context(canvas);
    if (!data || !width || !height) return;
    const x = (absolute) => (absolute - first()) * width / length();
    for (const run of data.coverage) {
      paint.fillStyle = fills[run.status];
      paint.fillRect(x(run.picture_row_start), 0, x(run.picture_row_end) - x(run.picture_row_start), height);
    }
    paint.fillStyle = rangeTint;
    paint.fillRect(x(range.start), 0, x(range.end) - x(range.start), height);
    for (let frame = data.window.start + 1; frame < data.window.end; frame++) {
      vertical(paint, x(frame * data.rows_per_frame), height, '#858477', true);
    }
    for (let channel = 0; channel < 2; channel++) {
      const middle = (channel * .5 + .25) * height;
      paint.fillStyle = '#748276';
      paint.fillRect(0, middle, width, 1);
      paint.save();
      paint.beginPath();
      for (const run of data.coverage) {
        if (run.status === 'readable') {
          paint.rect(x(run.picture_row_start), 0, x(run.picture_row_end) - x(run.picture_row_start), height);
        }
      }
      paint.clip();
      for (const bin of data.envelope) {
        if (!bin.readable) continue;
        const binX = x(bin.start);
        const y = middle - bin.max[channel] / 32767 * height * .215;
        const extent = (bin.max[channel] - bin.min[channel]) / 32767 * height * .215;
        paint.fillStyle = channel === 0 ? '#efa66e' : '#a7ccaa';
        paint.fillRect(binX, y, Math.max(1, x(bin.end) - binX), Math.max(1, extent));
      }
      paint.restore();
    }
    for (const absolute of [range.start, range.end]) {
      const hx = x(absolute);
      paint.fillStyle = handleColor;
      paint.fillRect(hx - 1, 0, 2, height);
      paint.fillRect(Math.min(Math.max(hx - 3.5, 0), width - 7), 2, 7, 10);
    }
    if (cursor !== undefined) vertical(paint, Math.max(1, x(cursor + .5)), height, '#f6a069');
    if (playhead !== undefined) vertical(paint, x(playhead), height, '#fbf5e6', true);
    if (drag) tooltip(paint, width, x(drag.row), readout(drag.row));
  }

  function drawRuler() {
    const { paint, width, height } = context(rulerCanvas);
    if (!data || !width || !height) return;
    const rows = data.rows_per_frame;
    const x = (absolute) => (absolute - first()) * width / length();
    for (const run of data.coverage) {
      if (run.status === 'readable') continue;
      paint.fillStyle = gapTint[run.status];
      paint.fillRect(x(run.picture_row_start), 0, x(run.picture_row_end) - x(run.picture_row_start), height);
    }
    paint.font = '10px ui-monospace, monospace';
    paint.textBaseline = 'alphabetic';
    const step = Math.max(1, Math.ceil(34 / (rows * width / length())));
    const tick = (tx) => {
      paint.strokeStyle = '#858477';
      paint.beginPath();
      paint.moveTo(tx, 0);
      paint.lineTo(tx, 5);
      paint.stroke();
    };
    const place = (tx, text, color, align = 'left') => {
      const w = paint.measureText(text).width;
      const lx = align === 'center' ? tx - w / 2 : tx;
      paint.fillStyle = color;
      paint.fillText(text, Math.min(Math.max(lx, 1), width - w - 1), height - 4);
    };
    if (mode === 'frames') {
      for (let frame = data.window.start; frame <= data.window.end; frame++) {
        tick(x(frame * rows));
        if (frame < data.window.end && (frame - data.window.start) % step === 0) {
          place(x(frame * rows) + 2, `${frame} 格`, '#cac9b9');
        }
      }
    } else {
      const lo = Math.floor((first() + lead()) / rows);
      const hi = Math.floor((last() + lead()) / rows);
      for (let page = lo; page <= hi; page++) {
        const boundary = page * rows - lead();
        const spanStart = Math.max(first(), boundary);
        const spanEnd = Math.min(last(), boundary + rows);
        if (spanStart >= spanEnd) continue;
        if (boundary > first() && boundary < last()) tick(x(boundary));
        const center = (spanStart + spanEnd) / 2;
        const px = x(spanEnd) - x(spanStart);
        if (runAt(center).status === 'missing_sheet') {
          if (px >= 12) place(x(center), '缺', '#d98a7d', 'center');
        } else if (runAt(center).status === 'readable' && (page - lo) % step === 0) {
          place(x(center), `${page} 页`, '#cac9b9', 'center');
        }
      }
    }
    paint.fillStyle = handleColor;
    for (const absolute of [range.start, range.end]) {
      const tx = Math.min(Math.max(x(absolute), 3), width - 3);
      paint.beginPath();
      paint.moveTo(tx - 3.5, 0);
      paint.lineTo(tx + 3.5, 0);
      paint.lineTo(tx, 6);
      paint.fill();
    }
  }

  function rowAt(event) {
    return Math.min(last(), Math.max(first(), first() + Math.round(fraction(event, canvas) * length())));
  }

  function handleNear(event) {
    const { left, width } = canvas.getBoundingClientRect();
    const px = event.clientX - left;
    const at = (absolute) => (absolute - first()) * width / length();
    const ds = Math.abs(px - at(range.start));
    const de = Math.abs(px - at(range.end));
    if (Math.min(ds, de) > grabPx) return null;
    return ds <= de ? 'start' : 'end';
  }

  canvas.addEventListener('pointerdown', (event) => {
    if (!data || event.button !== 0) return;
    event.preventDefault();
    canvas.setPointerCapture(event.pointerId);
    const kind = handleNear(event);
    if (kind) {
      drag = { kind, row: kind === 'start' ? range.start : range.end };
      canvas.style.cursor = 'ew-resize';
    } else {
      const anchor = Math.min(rowAt(event), last() - 1);
      drag = { kind: 'draft', anchor, row: anchor, downX: event.clientX, downY: event.clientY, moved: false };
    }
  });

  canvas.addEventListener('pointermove', (event) => {
    if (!data) return;
    if (!drag) {
      canvas.style.cursor = handleNear(event) ? 'ew-resize' : 'crosshair';
      return;
    }
    if (drag.kind === 'draft' && !drag.moved) {
      if (Math.hypot(event.clientX - drag.downX, event.clientY - drag.downY) <= clickSlop) return;
      drag.moved = true;
    }
    const row = rowAt(event);
    if (drag.kind === 'start') {
      range.start = Math.min(row, range.end - 1);
      drag.row = range.start;
    } else if (drag.kind === 'end') {
      range.end = Math.max(row, range.start + 1);
      drag.row = range.end;
    } else {
      const r = Math.min(row, last() - 1);
      drag.row = r;
      range = r === drag.anchor
        ? { start: r, end: r + 1 }
        : { start: Math.min(drag.anchor, r), end: Math.max(drag.anchor, r) };
    }
    commit();
  });

  function finish(event, cancelled) {
    if (!drag) return;
    const done = drag;
    drag = undefined;
    canvas.style.cursor = 'crosshair';
    if (done.kind === 'draft' && !done.moved && !cancelled) {
      locate(first() + Math.min(length() - 1, Math.floor(fraction(event, canvas) * length())));
      return;
    }
    commit();
  }
  canvas.addEventListener('pointerup', (event) => finish(event, false));
  canvas.addEventListener('pointercancel', (event) => finish(event, true));

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (!data) return;
    const rows = data.rows_per_frame;
    const endpoint = (prefix) => {
      const a = form.elements[`${prefix}-a`];
      const b = form.elements[`${prefix}-b`];
      if (a.value.trim() === '' && b.value.trim() === '') return { keep: true };
      const va = a.valueAsNumber;
      const vb = b.valueAsNumber;
      if (!Number.isInteger(va) || !Number.isInteger(vb)) return { error: '请把两个读数都填成整数。' };
      if (vb < 0 || vb >= rows) return { error: `行号应在 0 到 ${rows - 1} 之间。` };
      return { absolute: mode === 'frames' ? va * rows + vb : va * rows + vb - lead() };
    };
    const start = endpoint('start');
    const end = endpoint('end');
    if (start.error || end.error) return rangeError(start.error || end.error);
    if (start.keep && end.keep) return rangeError('两个端点都未填写，没有可应用的改动。');
    const next = { start: start.keep ? range.start : start.absolute, end: end.keep ? range.end : end.absolute };
    if (next.start < first() || next.start >= last() || next.end <= first() || next.end > last()) {
      return rangeError('端点超出当前窗口。');
    }
    if (mode === 'sound') {
      if ((!start.keep && runAt(next.start).status !== 'readable') || (!end.keep && runAt(next.end).status !== 'readable')) {
        return rangeError('该位置没有声槽页读数（缺页或卷外），请换画格尺放置此端。');
      }
    }
    if (next.start >= next.end) return rangeError('起点必须早于终点。');
    range = next;
    commit();
  });

  reset.addEventListener('click', () => {
    if (!data) return;
    range = { start: first(), end: last() };
    commit();
  });

  for (const button of toggles) {
    button.addEventListener('click', () => {
      if (mode === button.dataset.ruler) return;
      mode = button.dataset.ruler;
      applyMode();
    });
  }

  new ResizeObserver(() => { draw(); drawRuler(); }).observe(canvas);
  new ResizeObserver(drawRuler).observe(rulerCanvas);

  return {
    setWindow(windowData) {
      data = windowData;
      cursor = playhead = drag = undefined;
      range = { start: first(), end: last() };
      clock.textContent = `${compactNumber(length())} 个采样位置 · ${compactNumber(length() * 1000 / data.rate)} 毫秒`;
      const runs = data.coverage.map((run) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = `${statusName[run.status]} · ${position(run.picture_row_start, data.rows_per_frame)}起 · ${run.picture_row_end - run.picture_row_start} 行`;
        button.addEventListener('click', () => locate(run.picture_row_start));
        return button;
      });
      list.replaceChildren(...runs);
      applyMode();
      draw();
      for (const listener of listeners) listener(range.start, range.end);
    },
    select(frame, row) { cursor = frame * data.rows_per_frame + row; draw(); },
    playback(absolute) { playhead = absolute; draw(); },
    onRange(listener) { listeners.push(listener); },
  };
}
