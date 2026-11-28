import { benchURL, compactNumber } from './client.js';
import { context, fraction, vertical } from './canvas.js';

export function film(root, onPosition) {
  const rail = root.querySelector('#film-rail');
  const picture = root.querySelector('#scan-picture');
  const marker = root.querySelector('#strip-marker');
  const placeholder = root.querySelector('#strip-status');
  const summary = root.querySelector('#frame-summary');
  const sheetStatus = root.querySelector('#card-status');
  let data, selected, row, sheetURL, sheetToken = 0;
  let buttons = new Map();

  function draw() {
    const { paint, width, height } = context(marker);
    if (data && row !== undefined) {
      vertical(paint, Math.max(1, (row + .5) * width / data.rows_per_frame), height, '#f6a069');
    }
  }

  // Each card is a same-size crop of one contact sheet. Crops are only shown
  // once the sheet for this exact window and profile has loaded, so a sheet
  // from an earlier calibration can never sit under the current labels.
  function placeCards() {
    const layout = data.cards;
    rail.style.setProperty('--card-sheet', `url("${sheetURL}")`);
    for (const [index, button] of [...buttons.values()].entries()) {
      const card = button.querySelector('.card-image');
      card.style.width = `${layout.card_width}px`;
      card.style.height = `${layout.card_height}px`;
      card.style.backgroundPosition =
        `${-(index % layout.columns) * layout.card_width}px ${-Math.floor(index / layout.columns) * layout.card_height}px`;
    }
  }

  function clearSheet(message) {
    sheetURL = undefined;
    rail.style.removeProperty('--card-sheet');
    sheetStatus.hidden = !message;
    sheetStatus.textContent = message ?? '';
  }

  function loadSheet(attempt = 0) {
    const token = ++sheetToken;
    const url = benchURL(data.reel, 'cards', data.params);
    clearSheet('正在生成诊断画格…');
    const loader = new Image();
    loader.onload = () => {
      if (token !== sheetToken) return;
      sheetURL = url;
      placeCards();
      sheetStatus.hidden = true;
    };
    loader.onerror = () => {
      if (token !== sheetToken) return;
      // One retry covers a sheet slot that is briefly busy. After that the
      // sheet is only a convenience: frame buttons, strip and probe keep
      // working from the applied window while it is unavailable.
      if (attempt === 0) {
        window.setTimeout(() => {
          if (token === sheetToken) loadSheet(1);
        }, 700);
        return;
      }
      clearSheet('诊断画格暂不可用，可直接点选画格。');
    };
    loader.src = url;
  }

  function selectFrame(frame) {
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
        button.setAttribute('aria-pressed', 'false');
        const image = document.createElement('span');
        image.className = 'card-image';
        image.setAttribute('aria-hidden', 'true');
        const label = document.createElement('span');
        label.className = 'card-label';
        label.textContent = `第 ${String(frame.frame).padStart(2, '0')} 格`;
        const state = document.createElement('small');
        state.textContent = frame.depth ? `${frame.depth} 位` : '画面缺页';
        button.toggleAttribute('data-gap', !frame.depth);
        button.setAttribute('aria-label', `第 ${frame.frame} 格，${frame.depth ? frame.depth + ' 位灰阶画面' : '画面缺页'}，${frame.readable} 行可读声槽，${frame.missing} 行声槽缺页，${frame.outside} 行越界`);
        button.title = button.getAttribute('aria-label');
        button.append(image, label, state);
        button.addEventListener('click', () => onPosition(frame.frame, 0));
        buttons.set(frame.frame, button);
        return button;
      });
      rail.replaceChildren(...elements);
      rail.style.gridTemplateColumns = `repeat(${data.cards.columns}, 5rem)`;
      loadSheet();
    },
    select(frame, nextRow) {
      row = nextRow;
      if (selected !== frame) selectFrame(frame);
      draw();
    },
  };
}
