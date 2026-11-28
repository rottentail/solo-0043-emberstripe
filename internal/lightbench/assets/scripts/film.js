import { benchURL, compactNumber } from './client.js';
import { PICTURE, PENDING, FAILED, STALE, VERIFIED, ABSENT } from './evidence.js';
import { context, fraction, vertical } from './canvas.js';

export function film(root, onPosition, ledger) {
  const rail = root.querySelector('#film-rail');
  const picture = root.querySelector('#scan-picture');
  const marker = root.querySelector('#strip-marker');
  const placeholder = root.querySelector('#strip-status');
  const summary = root.querySelector('#frame-summary');
  let data, selected, row;
  let buttons = new Map();
  let loadedKey = null;

  function draw() {
    const { paint, width, height } = context(marker);
    if (data && row !== undefined) {
      vertical(paint, Math.max(1, (row + .5) * width / data.rows_per_frame), height, '#f6a069');
    }
  }

  marker.addEventListener('click', (event) => {
    if (selected === undefined) return;
    onPosition(selected, Math.min(data.rows_per_frame - 1, Math.floor(fraction(event, marker) * data.rows_per_frame)));
  });
  new ResizeObserver(draw).observe(marker);

  // The ledger owns picture fetching and its context key. The strip only mirrors
  // the picture piece so a failed projection never erases recoverable sound.
  function renderPicture() {
    if (!ledger || selected === undefined) return;
    const piece = ledger.piece(PICTURE);
    const key = `${data.reel}|picture|${selected}`;
    if (piece.key !== key) return;
    const entry = data.frames[selected - data.window.start];
    if (piece.state === ABSENT || !entry.depth) {
      picture.hidden = true;
      picture.removeAttribute('src');
      loadedKey = null;
      placeholder.hidden = false;
      placeholder.textContent = '此画格没有画面扫描页；声槽按偏移后的位置仍可单独读取。';
      return;
    }
    if (piece.state === PENDING || piece.state === STALE) {
      picture.hidden = true;
      placeholder.hidden = false;
      placeholder.textContent = '正在读取扫描页…';
      return;
    }
    if (piece.state === FAILED) {
      picture.hidden = true;
      placeholder.hidden = false;
      placeholder.textContent = `扫描页暂时读不回来（${piece.error ?? '可在证据恢复中重取'}）。声槽证据不受影响。`;
      return;
    }
    if (piece.state === VERIFIED) {
      if (loadedKey !== key) {
        picture.src = benchURL(data.reel, 'strip', { frame: selected });
        loadedKey = key;
      }
      picture.hidden = false;
      placeholder.hidden = true;
    }
  }
  ledger?.subscribe(renderPicture);

  return {
    setWindow(windowData) {
      data = windowData;
      selected = undefined;
      loadedKey = null;
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
        loadedKey = null;
        placeholder.hidden = false;
        placeholder.textContent = entry.depth ? '正在读取扫描页…' : '此画格没有画面扫描页；声槽按偏移后的位置仍可单独读取。';
      }
      draw();
      renderPicture();
    },
  };
}
