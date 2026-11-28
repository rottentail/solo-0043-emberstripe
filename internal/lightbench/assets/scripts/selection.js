import { compactNumber, position } from './client.js';

// selection owns the continuous-check end points. They are absolute picture
// rows and the last row is inclusive. End points only change when the operator
// presses one of these buttons; playback, seeking and redraws never rewrite
// them, so a stalled page cannot quietly extend the chosen end.
export function selection(root, onChange) {
  const startButton = root.querySelector('#check-start');
  const endButton = root.querySelector('#check-end');
  const clearButton = root.querySelector('#check-clear');
  const summary = root.querySelector('#check-summary');
  let data, cursor, start, end;

  const range = () => (start === undefined ? null : { start, end });

  function describe() {
    if (!data) return '载入画格窗后可设定连续检查选区。';
    if (start === undefined) return '在探针定位后，设定连续检查的起点与末行（含末行）。';
    if (end === undefined) return `起点 ${position(start, data.rows_per_frame)} 已设 · 继续定位并设为末行。`;
    const rows = end - start + 1;
    return `连续检查 ${position(start, data.rows_per_frame)} → ${position(end, data.rows_per_frame)}（含末行）`
      + ` · ${rows} 行 / ${compactNumber(rows * 1000 / data.rate)} 毫秒 · 穿过缺口仍按原画格时钟计时。`;
  }

  function refresh() {
    startButton.disabled = !data;
    endButton.disabled = !data || start === undefined;
    clearButton.disabled = start === undefined;
    summary.textContent = describe();
  }

  function emit() {
    refresh();
    onChange?.(range());
  }

  startButton.addEventListener('click', () => {
    if (!data || !cursor) return;
    start = cursor.absolute;
    const dropped = end !== undefined && end < start;
    if (dropped) end = undefined;
    emit();
    if (dropped) summary.textContent = `起点 ${position(start, data.rows_per_frame)} 晚于原末行，原末行已清除；请重新定位末行。`;
  });

  endButton.addEventListener('click', () => {
    if (!data || !cursor || start === undefined) return;
    if (cursor.absolute < start) {
      summary.textContent = '末行不能早于起点；原选区保持不变。';
      return;
    }
    end = cursor.absolute;
    emit();
  });

  clearButton.addEventListener('click', () => {
    if (!data) return;
    start = end = undefined;
    emit();
    summary.textContent = '已清除连续检查选区。';
  });

  return {
    range,
    setWindow(windowData) {
      data = windowData;
      cursor = undefined;
      start = end = undefined;
      refresh();
    },
    select(frame, row) {
      if (!data || frame < data.window.start || frame >= data.window.end || row < 0 || row >= data.rows_per_frame) return;
      cursor = { frame, row, absolute: frame * data.rows_per_frame + row };
      refresh();
    },
  };
}
