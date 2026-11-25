import { compactNumber, position } from './client.js';
import { context, fraction, vertical } from './canvas.js';

const weakRows = 8;
const persistentFraction = 0.8;

export function edge(root, onPosition) {
  const canvas = root.querySelector('#edge-canvas');
  const summary = root.querySelector('#edge-summary');
  const readout = root.querySelector('#edge-readout');
  let data, cursor, hover;
  const first = () => data.window.start * data.rows_per_frame;
  const length = () => (data.window.end - data.window.start) * data.rows_per_frame;
  const x = (absolute) => (absolute - first()) * canvas.clientWidth / length();

  // A bin supports a skew conclusion only when it has enough rows and every
  // contributing sheet uses the same native bit depth.
  function supported(bin) {
    if (bin.readable < weakRows) return false;
    return Object.keys(bin.depths).length === 1;
  }

  function locate(event) {
    const absolute = first() + Math.min(length() - 1, Math.floor(fraction(event, canvas) * length()));
    onPosition(Math.floor(absolute / data.rows_per_frame), absolute % data.rows_per_frame);
  }

  function binAt(clientX) {
    const point = (clientX - canvas.getBoundingClientRect().left) / canvas.clientWidth * length() + first();
    return data.edge_balance.find((bin) => point >= bin.start && point < bin.end);
  }

  function describe(bin) {
    if (!bin) return '';
    const where = `${position(bin.start, data.rows_per_frame)}起 · ${bin.end - bin.start} 行`;
    if (!bin.readable) return `${where}：缺页或卷外，没有边缘依据。`;
    const depths = Object.entries(bin.depths).map(([depth, rows]) => `${depth} 位 ${rows} 行`).join('、');
    const lines = [`${where} · 可读 ${bin.readable} 行（${depths}）`];
    for (const [lane, name] of ['左槽', '右槽'].entries()) {
      const edge = bin.lanes[lane];
      if (!edge.edge_width) { lines.push(`${name}：宽度不足，无法分边。`); continue; }
      const side = edge.mean > 0 ? '偏右亮' : edge.mean < 0 ? '偏左亮' : '均衡';
      const reliable = supported(bin);
      const steady = edge.persistent >= persistentFraction ? '持续同向' : '方向反复';
      lines.push(`${name}：归一差 ${compactNumber(edge.mean)}（${side}，${steady}），峰值 ${compactNumber(edge.peak)}，${reliable ? '依据充分' : '依据不足'}`);
    }
    return lines.join('；');
  }

  function draw() {
    const { paint, width, height } = context(canvas);
    if (!data || !width || !height) return;
    for (const run of data.coverage) {
      paint.fillStyle = run.status === 'readable' ? '#243029' : run.status === 'missing_sheet' ? '#3c2d29' : '#32332f';
      paint.fillRect(x(run.picture_row_start), 0, x(run.picture_row_end) - x(run.picture_row_start), height);
    }
    for (let frame = data.window.start + 1; frame < data.window.end; frame++) {
      vertical(paint, x(frame * data.rows_per_frame), height, '#7c7b6f', true);
    }
    for (let lane = 0; lane < 2; lane++) {
      const band = height / 2;
      const middle = band * (lane + .5);
      paint.save();
      paint.beginPath();
      paint.rect(0, lane * band, width, band);
      paint.clip();
      paint.strokeStyle = '#748276';
      paint.beginPath();
      paint.moveTo(0, middle);
      paint.lineTo(width, middle);
      paint.stroke();
      for (const bin of data.edge_balance) {
        const edge = bin.lanes[lane];
        if (!edge.rows || !edge.edge_width) continue;
        const from = x(bin.start);
        const barWidth = Math.max(1, x(bin.end) - from);
        const magnitude = Math.min(1, Math.abs(edge.mean)) * band * .42;
        const top = edge.mean >= 0 ? middle - magnitude : middle;
        paint.fillStyle = lane === 0 ? '#efa66e' : '#a7ccaa';
        paint.globalAlpha = supported(bin) ? (0.35 + 0.65 * edge.persistent) : 0.32;
        paint.fillRect(from, top, barWidth, Math.max(1, magnitude));
        paint.globalAlpha = 1;
        if (!supported(bin)) {
          paint.strokeStyle = '#d8d2bd';
          paint.lineWidth = 1;
          paint.beginPath();
          for (let step = top; step < top + Math.max(1, magnitude); step += 4) {
            paint.moveTo(from, step);
            paint.lineTo(from + barWidth, step + barWidth);
          }
          paint.stroke();
        }
      }
      paint.restore();
    }
    if (cursor !== undefined) vertical(paint, Math.max(1, x(cursor + .5)), height, '#f6a069');
  }

  canvas.addEventListener('click', (event) => { if (data) locate(event); });
  canvas.addEventListener('mousemove', (event) => {
    if (!data) return;
    hover = binAt(event.clientX);
    readout.textContent = describe(hover);
  });
  canvas.addEventListener('mouseleave', () => { hover = undefined; readout.textContent = '悬停分桶查看边缘差、位深和可读行数。'; });
  new ResizeObserver(draw).observe(canvas);

  return {
    setWindow(windowData) {
      data = windowData;
      cursor = undefined;
      const lanes = data.lane_widths.map((width, lane) => `${width === 1 ? '单列' : `半宽 ${data.edge_balance[0]?.lanes[lane].edge_width ?? 0} 列`}`).join(' · ');
      summary.textContent = `左槽 ${data.lane_widths[0]} 列 / 右槽 ${data.lane_widths[1]} 列（${lanes}）`;
      readout.textContent = '悬停分桶查看边缘差、位深和可读行数。';
      draw();
    },
    select(frame, row) {
      cursor = frame * data.rows_per_frame + row;
      draw();
      if (!hover) readout.textContent = describe(data.edge_balance.find((bin) => cursor >= bin.start && cursor < bin.end));
    },
  };
}
