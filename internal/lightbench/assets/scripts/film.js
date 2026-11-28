import { benchURL, compactNumber } from './client.js';
import { context, fraction, vertical } from './canvas.js';

export function film(root, onPosition) {
  const rail = root.querySelector('#film-rail');
  const picture = root.querySelector('#scan-picture');
  const marker = root.querySelector('#strip-marker');
  const placeholder = root.querySelector('#strip-status');
  const summary = root.querySelector('#frame-summary');
  let data, selected, row;
  let buttons = new Map();

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

  return {
    setWindow(windowData) {
      data = windowData;
      selected = undefined;
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
    // mark tracks a skimmed cursor without issuing a new strip fetch; the
    // strip image and detailed evidence wait for the release settle.
    mark(frame, nextRow) {
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
        placeholder.textContent = entry.depth ? '松手后读取扫描页…' : '此画格没有画面扫描页';
      }
      draw();
    },
  };
}
