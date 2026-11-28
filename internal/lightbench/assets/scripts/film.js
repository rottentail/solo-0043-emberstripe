import { benchURL, compactNumber } from './client.js';
import { context, fraction, vertical } from './canvas.js';

export function film(root, onPosition) {
  const rail = root.querySelector('#film-rail');
  const picture = root.querySelector('#scan-picture');
  const marker = root.querySelector('#strip-marker');
  const placeholder = root.querySelector('#strip-status');
  const summary = root.querySelector('#frame-summary');
  let data, selected, row, playRow, range;
  let buttons = new Map();
  let playedFrame;

  const frameOf = (absolute) => Math.floor(absolute / data.rows_per_frame);

  // The rail marks whatever frame playback is passing with a light outline. This
  // is only a played-position indicator; the selected frame and the chosen
  // range are drawn separately and are never overwritten by playback.
  function markRail() {
    const current = playRow === undefined ? undefined : frameOf(playRow);
    if (current === playedFrame) return;
    playedFrame = current;
    for (const [frame, button] of buttons) {
      button.toggleAttribute('data-playing', frame === current);
    }
  }

  function markRange() {
    for (const [frame, button] of buttons) {
      const inRange = range && range.end !== undefined
        && frame >= frameOf(range.start) && frame <= frameOf(range.end);
      button.toggleAttribute('data-range', Boolean(inRange));
    }
  }

  function followRail() {
    if (playRow === undefined) return;
    buttons.get(frameOf(playRow))?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }

  function draw() {
    const { paint, width, height } = context(marker);
    if (!data || selected === undefined) return;
    const base = selected * data.rows_per_frame;
    const rows = data.rows_per_frame;
    // The strip projection shows one picture frame, so overlays are drawn in
    // that frame's local rows even though the range is absolute.
    if (range) {
      const top = Math.max(range.start, base) - base;
      const bottom = Math.min((range.end ?? range.start) + 1, base + rows) - base;
      if (bottom > top) {
        paint.fillStyle = 'rgba(246, 160, 105, .18)';
        paint.fillRect(0, top * height / rows, width, (bottom - top) * height / rows);
      }
    }
    if (row !== undefined) {
      vertical(paint, Math.max(1, (row + .5) * width / rows), height, '#f6a069');
    }
    if (playRow !== undefined && frameOf(playRow) === selected) {
      vertical(paint, Math.max(1, (playRow - base + .5) * width / rows), height, '#fbf5e6', true);
    }
  }

  marker.addEventListener('click', (event) => {
    if (selected === undefined) return;
    onPosition(selected, Math.min(data.rows_per_frame - 1, Math.floor(fraction(event, marker) * data.rows_per_frame)));
  });
  new ResizeObserver(draw).observe(marker);

  return {
    setWindow(windowData) {
      data = windowData;
      selected = undefined;
      row = undefined;
      playRow = undefined;
      range = undefined;
      playedFrame = undefined;
      buttons = new Map();
      const elements = data.frames.map((frame) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = `第 ${String(frame.frame).padStart(2, '0')} 格`;
        button.setAttribute('aria-pressed', 'false');
        const state = document.createElement('span');
        state.textContent = frame.depth ? `${frame.depth} 位灰阶` : '画面缺页';
        button.toggleAttribute('data-gap', !frame.depth);
        button.append(state);
        button.addEventListener('click', () => onPosition(frame.frame, 0));
        buttons.set(frame.frame, button);
        return button;
      });
      rail.replaceChildren(...elements);
      // A fresh window rebuilds the buttons, so re-apply both marks once.
      playedFrame = undefined;
      markRail();
      markRange();
    },
    setRange(next) {
      range = next ?? undefined;
      markRange();
      draw();
    },
    select(frame, nextRow) {
      row = nextRow;
      if (selected !== frame) {
        buttons.get(selected)?.setAttribute('aria-pressed', 'false');
        selected = frame;
        buttons.get(frame)?.setAttribute('aria-pressed', 'true');
        const entry = data.frames[frame - data.window.start];
        summary.textContent = `第 ${frame} 格 · ${compactNumber(entry.readable)} 行声槽可读`;
        picture.hidden = true;
        picture.onload = null;
        picture.onerror = null;
        placeholder.hidden = false;
        if (entry.depth) {
          placeholder.textContent = '正在读取扫描页…';
          picture.onload = () => { picture.hidden = false; placeholder.hidden = true; };
          picture.onerror = () => { placeholder.textContent = '暂时无法显示扫描页。'; };
          picture.src = benchURL(data.reel, 'strip', { frame });
        } else {
          picture.removeAttribute('src');
          placeholder.textContent = '此画格没有画面扫描页';
        }
      }
      draw();
    },
    // playback draws the exact picture row passed by the playing clock. It
    // scrolls the rail to the frame but never changes the selected frame.
    playback(absolute) {
      if (!data) return;
      if (absolute === undefined) {
        playRow = undefined;
        markRail();
        draw();
        return;
      }
      const next = Math.max(data.window.start * data.rows_per_frame,
        Math.min(data.window.end * data.rows_per_frame - 1, absolute));
      if (next === playRow) return;
      const frameChanged = frameOf(next) !== playedFrame;
      playRow = next;
      markRail();
      if (frameChanged) followRail();
      draw();
    },
  };
}
