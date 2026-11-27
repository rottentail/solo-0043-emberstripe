import { compactNumber, position, statusName } from './client.js';
import { context, fraction, vertical } from './canvas.js';

const fills = { readable: '#29392f', missing_sheet: '#58352e', outside_capture: '#484741' };

export function scope(root, onPosition) {
  const canvas = root.querySelector('#waveform');
  const clock = root.querySelector('#scope-clock');
  const list = root.querySelector('#coverage-list');
  let data, cursor, playhead;
  const first = () => data.window.start * data.rows_per_frame;
  const length = () => (data.window.end - data.window.start) * data.rows_per_frame;
  const visible = (absolute) => Number.isFinite(absolute) && absolute >= first() && absolute < first() + length();

  function locate(absolute) {
    onPosition(Math.floor(absolute / data.rows_per_frame), absolute % data.rows_per_frame);
  }

  function draw() {
    const { paint, width, height } = context(canvas);
    if (!data || !width || !height) return;
    const x = (absolute) => (absolute - first()) * width / length();
    for (const run of data.coverage) {
      paint.fillStyle = fills[run.status];
      paint.fillRect(x(run.picture_row_start), 0, x(run.picture_row_end) - x(run.picture_row_start), height);
    }
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
    // A carried cursor or playhead outside this window is not drawn; the probe
    // panel names it and offers the relocation instead.
    if (visible(cursor)) vertical(paint, Math.max(1, x(cursor + .5)), height, '#f6a069');
    if (visible(playhead)) vertical(paint, x(playhead), height, '#fbf5e6', true);
  }

  canvas.addEventListener('click', (event) => {
    if (data) locate(first() + Math.min(length() - 1, Math.floor(fraction(event, canvas) * length())));
  });
  new ResizeObserver(draw).observe(canvas);

  return {
    setWindow(windowData) {
      data = windowData;
      cursor = playhead = undefined;
      clock.textContent = `${compactNumber(length())} 个采样位置 · ${compactNumber(length() * 1000 / data.rate)} 毫秒`;
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
    select(frame, row) { cursor = frame * data.rows_per_frame + row; draw(); },
    playback(absolute) { playhead = absolute; draw(); },
  };
}
