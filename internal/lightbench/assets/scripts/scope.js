import { benchURL, compactNumber, position, request, statusName } from './client.js';
import { context, fraction, vertical } from './canvas.js';

const fills = { readable: '#29392f', missing_sheet: '#58352e', outside_capture: '#484741' };

// Zoom controls how many picture rows fill the plot. The narrowest view keeps a
// handful of rows on screen so a pulse lasting a few rows stays legible; the
// widest view is the whole applied window.
const minRows = 8;
const zoomStep = 1.25;

export function scope(root, onPosition) {
  const canvas = root.querySelector('#waveform');
  const clock = root.querySelector('#scope-clock');
  const badge = root.querySelector('#scope-view');
  const zoomIn = root.querySelector('#zoom-in');
  const zoomOut = root.querySelector('#zoom-out');
  const zoomReset = root.querySelector('#zoom-reset');
  const list = root.querySelector('#coverage-list');
  let data, cursor, playhead, view, detail, pending;

  const bounds = () => [
    data.window.start * data.rows_per_frame,
    data.window.end * data.rows_per_frame,
  ];
  // `view` is the requested local interval and updates synchronously so rapid
  // wheel steps compound instead of each re-reading the previous reply.
  // `detail` is the most recent binned reply; its bins stay usable as a coarse
  // fallback until the reply for the newest view arrives.
  const first = () => view?.start ?? bounds()[0];
  const last = () => view?.end ?? bounds()[1];
  const length = () => last() - first();
  const bins = () => {
    if (!view) return data.envelope;
    // Never paint bins from a different interval under this view's mapping: a
    // stale reply would put spikes on rows they do not belong to. Until the
    // matching reply arrives, show only coverage.
    return detail?.detail.start === view.start && detail.detail.end === view.end ? detail.envelope : [];
  };

  function locate(absolute) {
    onPosition(Math.floor(absolute / data.rows_per_frame), absolute % data.rows_per_frame);
  }

  function clamp(start, end) {
    const [lo, hi] = bounds();
    // Keep the interval on whole rows so the server receives exactly the rows
    // the labels name; a fractional end would re-bin a sliver off by one.
    const width = Math.round(Math.max(minRows, Math.min(hi - lo, end - start)));
    start = Math.round(Math.min(Math.max(start, lo), hi - width));
    return { start, end: Math.min(hi, start + width) };
  }

  // Refining asks the station to re-bin the same applied window at the current
  // zoom. The reply carries bins and the rows behind their extrema, never raw
  // PCM, so widening the window cannot drag every sample into the browser and
  // narrowing it can name the row of a spike that sits at a gap edge.
  async function refine(start, end) {
    if (!data) return;
    view = clamp(start, end);
    describe();
    draw();
    pending?.abort();
    const controller = new AbortController();
    pending = controller;
    const params = { ...data.params, start_row: view.start, end_row: view.end };
    try {
      const result = await request(benchURL(data.reel, 'window', params), { signal: controller.signal });
      if (pending !== controller) return;
      detail = result;
      if (detail.detail.start === view.start && detail.detail.end === view.end) draw();
    } catch (error) {
      if (!controller.signal.aborted) clock.textContent = error.message;
    } finally {
      if (pending === controller) pending = undefined;
    }
  }

  function describe() {
    const span = length();
    const [lo, hi] = bounds();
    clock.textContent = `${compactNumber(span)} 行 · ${compactNumber(span * 1000 / data.rate)} 毫秒 · ${position(first(), data.rows_per_frame)}–${position(last() - 1, data.rows_per_frame)}`;
    if (!badge) return;
    const whole = span >= hi - lo;
    badge.textContent = whole ? '整窗' : `局部 ${Math.max(1, Math.round(100 * span / (hi - lo)))}%`;
    zoomReset.disabled = whole;
  }

  function draw() {
    const { paint, width, height } = context(canvas);
    if (!data || !width || !height) return;
    const x = (absolute) => (absolute - first()) * width / length();
    // Coverage bands are clipped to the view, then frame seams are drawn on top.
    for (const run of data.coverage) {
      const start = Math.max(first(), run.picture_row_start);
      const end = Math.min(last(), run.picture_row_end);
      if (start >= end) continue;
      paint.fillStyle = fills[run.status];
      paint.fillRect(x(start), 0, x(end) - x(start), height);
    }
    for (let frame = Math.floor(first() / data.rows_per_frame) + 1; frame * data.rows_per_frame < last(); frame++) {
      const edge = frame * data.rows_per_frame;
      if (edge > first()) vertical(paint, x(edge), height, '#858477', true);
    }
    // Bins never cross a coverage boundary, so a readable bin measures only
    // readable rows and its bar can only cover rows that can produce it.
    const measured = bins().filter((bin) => bin.end > first() && bin.start < last());
    for (let channel = 0; channel < 2; channel++) {
      const middle = (channel * .5 + .25) * height;
      paint.fillStyle = '#748276';
      paint.fillRect(0, middle, width, 1);
      paint.fillStyle = channel === 0 ? '#efa66e' : '#a7ccaa';
      for (const bin of measured) {
        if (!bin.readable) continue;
        const top = middle - bin.max[channel] / 32767 * height * .215;
        const extent = (bin.max[channel] - bin.min[channel]) / 32767 * height * .215;
        paint.fillRect(x(bin.start), top, Math.max(1, x(bin.end) - x(bin.start)), Math.max(1, extent));
      }
      spikeTicks(paint, x, width, measured, channel, middle, height);
    }
    if (cursor !== undefined && cursor >= first() && cursor < last()) {
      vertical(paint, Math.max(1, x(cursor + .5)), height, '#f6a069');
    }
    if (playhead !== undefined && playhead >= first() && playhead < last()) {
      vertical(paint, x(playhead), height, '#fbf5e6', true);
    }
  }

  // A tick marks the exact row of each bin's extrema. Once a bin is narrower
  // than a few pixels the bar alone is ambiguous; the ticks let an operator
  // read a short pulse back to the picture row that produced it, at either
  // polarity and on either side of a gap.
  function spikeTicks(paint, x, width, measured, channel, middle, height) {
    paint.fillStyle = channel === 0 ? '#f7c9a3' : '#cbe6cd';
    for (const bin of measured) {
      if (!bin.readable || x(bin.end) - x(bin.start) > 4) continue;
      for (const [row, value] of [[bin.max_row?.[channel], bin.max[channel]], [bin.min_row?.[channel], bin.min[channel]]]) {
        if (!Number.isInteger(row) || row < 0) continue;
        const top = middle - value / 32767 * height * .215;
        paint.fillRect(Math.max(0, Math.min(width - 1, x(row))), top - 1, 1, 2);
      }
    }
  }

  function zoom(factor, anchor = (first() + last()) / 2) {
    if (!data) return;
    const next = clamp(anchor - (anchor - first()) / factor, anchor + (last() - anchor) / factor);
    refine(next.start, next.end);
  }

  function click(event) {
    if (!data) return;
    locate(first() + Math.min(length() - 1, Math.floor(fraction(event, canvas) * length())));
  }

  canvas.addEventListener('click', click);
  canvas.addEventListener('wheel', (event) => {
    if (!data || (!event.ctrlKey && !event.metaKey)) return;
    event.preventDefault();
    const [lo, hi] = bounds();
    const anchor = Math.max(lo, Math.min(hi - 1, first() + fraction(event, canvas) * length()));
    zoom(event.deltaY < 0 ? zoomStep : 1 / zoomStep, anchor);
  }, { passive: false });
  canvas.addEventListener('dblclick', () => {
    const [lo, hi] = bounds();
    if (data) refine(lo, hi);
  });
  zoomIn?.addEventListener('click', () => zoom(zoomStep));
  zoomOut?.addEventListener('click', () => zoom(1 / zoomStep));
  zoomReset?.addEventListener('click', () => {
    const [lo, hi] = bounds();
    refine(lo, hi);
  });
  new ResizeObserver(draw).observe(canvas);

  return {
    setWindow(windowData) {
      data = windowData;
      view = undefined;
      detail = undefined;
      cursor = playhead = undefined;
      describe();
      const runs = data.coverage.map((run) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = `${statusName[run.status]} · ${position(run.picture_row_start, data.rows_per_frame)}起 · ${run.picture_row_end - run.picture_row_start} 行`;
        button.addEventListener('click', () => locate(run.picture_row_start));
        return button;
      });
      list.replaceChildren(...runs);
      draw();
    },
    select(frame, row) {
      cursor = frame * data.rows_per_frame + row;
      if (cursor < first() || cursor >= last()) {
        // Keep the selected row on screen without discarding the zoom level.
        const next = clamp(cursor - length() / 2, cursor + length() / 2);
        refine(next.start, next.end);
      } else {
        draw();
      }
    },
    playback(absolute) {
      playhead = absolute;
      draw();
    },
  };
}
