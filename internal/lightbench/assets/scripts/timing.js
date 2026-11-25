import { captureURL, request, position } from './client.js';

export function timing(root, waveform) {
  const form = root.querySelector('#timing-form');
  const marksBox = root.querySelector('#timing-marks');
  const wavButton = root.querySelector('#timing-wav-button');
  const sheetButton = root.querySelector('#timing-sheet-button');
  const media = root.querySelector('#timing-media');
  const status = root.querySelector('#timing-status');
  let data, marks = [], pending;

  function params() {
    const params = { ...data.params };
    for (const mark of marks) params.mark = `${mark.frame}:${mark.row}`;
    return params;
  }

  function renderMarks() {
    marksBox.replaceChildren(...marks.map((mark, index) => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'mark-chip';
      chip.textContent = `${position(mark.frame * data.rows_per_frame + mark.row, data.rows_per_frame)} · ${(mark.frame * data.rows_per_frame + mark.row - data.window.start * data.rows_per_frame) * 1000 / data.rate} ms`;
      chip.title = '移除该报点';
      chip.addEventListener('click', () => {
        marks.splice(index, 1);
        renderMarks();
      });
      return chip;
    }));
    const ready = data && marks.length > 0;
    wavButton.disabled = sheetButton.disabled = !ready;
    waveform.setMarkers(marks.map((mark) => mark.frame * data.rows_per_frame + mark.row));
  }

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (!data) return;
    const frame = form.elements.frame.valueAsNumber;
    const row = form.elements.row.valueAsNumber;
    if (!Number.isInteger(frame) || !Number.isInteger(row) || frame < data.window.start || frame >= data.window.end || row < 0 || row >= data.rows_per_frame) {
      status.textContent = `报点必须落在 ${data.window.start}–${data.window.end - 1} 格、0–${data.rows_per_frame - 1} 行之间。`;
      return;
    }
    if (marks.some((mark) => mark.frame === frame && mark.row === row)) {
      status.textContent = '该画格行已经有报点。';
      return;
    }
    marks.push({ frame, row });
    marks.sort((a, b) => (a.frame * data.rows_per_frame + a.row) - (b.frame * data.rows_per_frame + b.row));
    status.textContent = '';
    renderMarks();
  });

  async function validate() {
    pending?.abort();
    const controller = new AbortController();
    pending = controller;
    const sheet = await request(captureURL(data.reel, 'timing-sheet', params()), { signal: controller.signal });
    return sheet;
  }

  wavButton.addEventListener('click', async () => {
    if (!data || !marks.length) return;
    wavButton.disabled = true;
    status.textContent = '正在合成含报点的双声道计时带…';
    try {
      const sheet = await validate();
      media.hidden = false;
      media.src = captureURL(data.reel, 'timing', params());
      media.load();
      status.textContent = `已插入 ${sheet.markers.length} 个报点 · 窗长 ${sheet.sample_frames} 个采样帧 · 补零段 ${sheet.zero_pads.length} 处，详情请导出报点表。`;
    } catch (error) {
      status.textContent = `计时带导出失败。${error.message}`;
      wavButton.disabled = false;
    }
  });

  sheetButton.addEventListener('click', async () => {
    if (!data || !marks.length) return;
    sheetButton.disabled = true;
    status.textContent = '正在生成报点表…';
    try {
      const sheet = await validate();
      const blob = new Blob([JSON.stringify(sheet, null, 2)], { type: 'application/json' });
      const link = document.createElement('a');
      link.href = URL.createObjectURL(blob);
      link.download = `${data.reel}-timing-sheet.json`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(link.href), 4000);
      status.textContent = `报点表已导出：实际时间 = 采样帧 / ${sheet.sample_rate}，原卷画格行 = ${sheet.origin_picture_row} + 采样帧。`;
    } catch (error) {
      status.textContent = `报点表导出失败。${error.message}`;
    } finally {
      sheetButton.disabled = false;
    }
  });

  return {
    setWindow(windowData) {
      data = windowData;
      marks = [];
      pending?.abort();
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      form.elements.frame.min = data.window.start;
      form.elements.frame.max = data.window.end - 1;
      form.elements.row.max = data.rows_per_frame - 1;
      form.querySelector('button').disabled = false;
      status.textContent = '在上方波形或探针定位后插入报点，报点按画格时钟排序。';
      renderMarks();
    },
    select(frame, row) {
      if (!data) return;
      form.elements.frame.value = frame;
      form.elements.row.value = row;
    },
  };
}
