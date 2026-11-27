import { benchURL, compactNumber, request, statusName } from './client.js';
import { context, fraction, vertical } from './canvas.js';

// fragment magnifies the picture rows around the shared probe. Rows are mapped
// back from pointer position without relying on pixel width, so clicking either
// edge of the magnified image still lands on an exact frame row.
export function fragment(root, onPosition) {
  const open = root.querySelector('#fragment-open');
  const panel = root.querySelector('#fragment-panel');
  const spanSelect = root.querySelector('#fragment-span');
  const caption = root.querySelector('#fragment-caption');
  const image = root.querySelector('#fragment-image');
  const status = root.querySelector('#fragment-status');
  const marker = root.querySelector('#fragment-marker');
  const scaleFirst = root.querySelector('#fragment-scale-first');
  const scaleLast = root.querySelector('#fragment-scale-last');
  const depth = root.querySelector('#fragment-depth');
  const rowValue = root.querySelector('#fragment-row');
  const nativeValue = root.querySelector('#fragment-native');
  const displayValue = root.querySelector('#fragment-display');
  const quantizedValue = root.querySelector('#fragment-quantized');
  const soundNote = root.querySelector('#fragment-sound');

  let data;
  let selectedFrame;
  let currentRow = 0;
  let region;
  let active = false;
  let pending;

  function soundStatus(frame) {
    const entry = data.frames[frame - data.window.start];
    if (!entry) return '';
    if (entry.readable > 0) {
      return `此画格的偏移声槽仍有 ${compactNumber(entry.readable)} 行可读，画面缺页不影响波形、探针与试听。`;
    }
    const run = data.coverage.find((item) => frame * data.rows_per_frame >= item.picture_row_start
      && frame * data.rows_per_frame < item.picture_row_end);
    return run ? `当前画格声槽状态：${statusName[run.status]}，可继续在波形上检查相邻可读区段。` : '';
  }

  function showMissing(frame) {
    region = undefined;
    image.hidden = true;
    image.removeAttribute('src');
    status.hidden = false;
    status.textContent = '此画格没有画面扫描页，无法放大局部画面。';
    caption.textContent = `第 ${frame} 格 · 画面缺页`;
    scaleFirst.textContent = '';
    scaleLast.textContent = '';
    depth.textContent = '画面缺页';
    rowValue.textContent = '—';
    nativeValue.textContent = '—';
    displayValue.textContent = '—';
    quantizedValue.textContent = '—';
    soundNote.textContent = soundStatus(frame);
  }

  function paintMarker() {
    const { paint, width, height } = context(marker);
    if (!region) return;
    const span = region.row_end - region.row_start;
    const offset = region.selected_row - region.row_start + .5;
    vertical(paint, 1, height, '#858477', true);
    vertical(paint, width - 1, height, '#858477', true);
    vertical(paint, Math.max(1, offset * width / span), height, '#f6a069');
  }

  function render(payload) {
    region = payload;
    caption.textContent = `第 ${payload.frame} 格 · 行 ${payload.row_start}–${payload.row_end - 1} · ${payload.depth} 位源页`;
    scaleFirst.textContent = `第 ${payload.row_start} 行`;
    scaleLast.textContent = `第 ${payload.row_end - 1} 行`;
    status.hidden = true;
    image.hidden = false;
    image.onload = () => { image.hidden = false; status.hidden = true; };
    image.onerror = () => {
      image.hidden = true;
      status.hidden = false;
      status.textContent = '暂时无法显示局部片段。';
    };
    image.src = benchURL(data.reel, 'fragment-image', {
      frame: payload.frame, row: payload.selected_row, span: spanSelect.value,
    });
    const selected = payload.rows.find((item) => item.row === payload.selected_row);
    depth.textContent = `${payload.depth} 位灰阶`;
    rowValue.textContent = `${payload.selected_row} 行`;
    nativeValue.textContent = selected ? compactNumber(selected.native_gray) : '—';
    displayValue.textContent = selected ? compactNumber(selected.displayed_gray) : '—';
    quantizedValue.textContent = !selected ? '—'
      : selected.quantized_away ? '真实亮度被 8 位显示合并，存在不可见的低位灰阶' : '与 8 位显示一致';
    soundNote.textContent = '';
    paintMarker();
  }

  async function load(frame, row) {
    pending?.abort();
    const controller = new AbortController();
    pending = controller;
    caption.textContent = '正在读取局部片段…';
    try {
      const payload = await request(benchURL(data.reel, 'fragment', {
        frame, row, span: spanSelect.value,
      }), { signal: controller.signal });
      if (pending !== controller) return;
      render(payload);
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error.message.includes('没有画面扫描页')) {
        showMissing(frame);
      } else {
        status.hidden = false;
        image.hidden = true;
        status.textContent = error.message;
      }
    }
  }

  marker.addEventListener('click', (event) => {
    if (!region) return;
    const span = region.row_end - region.row_start;
    const row = region.row_start + Math.min(span - 1, Math.floor(fraction(event, marker) * span));
    onPosition(region.frame, row);
  });
  new ResizeObserver(paintMarker).observe(marker);

  spanSelect.addEventListener('change', () => {
    if (active && selectedFrame !== undefined) load(selectedFrame, currentRow);
  });

  open.addEventListener('click', () => {
    if (!data || selectedFrame === undefined) return;
    active = true;
    panel.hidden = false;
    open.textContent = '刷新局部片段';
    load(selectedFrame, currentRow);
  });

  return {
    setWindow(windowData) {
      data = windowData;
      active = false;
      region = undefined;
      selectedFrame = undefined;
      currentRow = 0;
      panel.hidden = true;
      open.disabled = false;
      open.textContent = '查看选中行附近局部片段';
    },
    select(frame, row) {
      selectedFrame = frame;
      currentRow = row;
      if (!active) return;
      if (region && frame === region.frame && row >= region.row_start && row < region.row_end) {
        region.selected_row = row;
        const selected = region.rows.find((item) => item.row === row);
        rowValue.textContent = `${row} 行`;
        nativeValue.textContent = selected ? compactNumber(selected.native_gray) : '—';
        displayValue.textContent = selected ? compactNumber(selected.displayed_gray) : '—';
        quantizedValue.textContent = !selected ? '—'
          : selected.quantized_away ? '真实亮度被 8 位显示合并，存在不可见的低位灰阶' : '与 8 位显示一致';
        paintMarker();
      } else {
        load(frame, row);
      }
    },
  };
}
