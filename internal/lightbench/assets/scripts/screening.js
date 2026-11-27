import { benchURL, captureURL, compactNumber, request, statusName } from './client.js';
import { context, fraction, vertical } from './canvas.js';

const fills = { readable: '#29392f', missing_sheet: '#58352e', outside_capture: '#484741' };
const edgeFill = '#6a4a25';

// screening previews the applied window on a projection clock. The axis is
// drawn in output samples; every frame boundary comes from the server's
// absolute frame number, so neighbouring windows join without dropping or
// repeating a frame when the rate ratio is not integral.
export function screening(root) {
  const form = root.querySelector('#screening-form');
  const clockNote = root.querySelector('#screening-clock');
  const status = root.querySelector('#screening-status');
  const canvas = root.querySelector('#screening-axis');
  const media = root.querySelector('#screening-media');
  const submit = form.querySelector('button');
  const readings = root.querySelector('#screening-readings');
  let applied, axis, pending, playhead, cursor;

  const value = (key, text = '—') => {
    readings.querySelector(`[data-value="${key}"]`).textContent = text;
  };
  const settings = () => ({ fps: form.elements.fps.valueAsNumber, rate: form.elements.rate.valueAsNumber });
  const sampleURL = (sample) => benchURL(applied.reel, 'preview/sample', {
    start: applied.window.start, end: applied.window.end, profile: applied.profile.profile, ...settings(), sample,
  });
  const audioURL = () => captureURL(applied.reel, 'preview', {
    start: applied.window.start, end: applied.window.end, profile: applied.profile.profile, ...settings(),
  });

  function seek(sample) {
    if (!axis) return;
    const clamped = Math.min(axis.clock.last_sample - 1, Math.max(axis.clock.first_sample, sample));
    if (!media.hidden && media.readyState >= 1) media.currentTime = (clamped - axis.clock.first_sample) / axis.clock.rate;
    show(clamped);
  }

  async function show(sample) {
    pending?.abort();
    const controller = new AbortController();
    pending = controller;
    try {
      const data = await request(sampleURL(sample), { signal: controller.signal });
      if (pending !== controller) return;
      const reading = data.sample;
      value('sample', `${compactNumber(sample)} · ${compactNumber(reading.window_time_ms)} 毫秒`);
      value('frame', `${reading.frame} 格 · 第 ${reading.local_frame_row} 行`);
      value('status', reading.status === 'conversion_edge'
        ? `转换边缘（${statusName[reading.from] ?? reading.from} → ${statusName[reading.to] ?? reading.to}）`
        : statusName[reading.status] ?? reading.status);
      value('source', reading.source ? `${reading.source.frame} 格 ${reading.source.row} 行` : '卷界外');
      value('pcm', `${reading.preview_sample.join(' / ')}　原声 ${reading.native_sample.join(' / ')}`);
      cursor = sample;
      draw();
    } catch (error) {
      if (!controller.signal.aborted) {
        status.dataset.error = '';
        status.textContent = error.message;
      }
    }
  }

  async function load(event) {
    event?.preventDefault();
    if (!applied || submit.disabled) return;
    const { fps, rate } = settings();
    if (!Number.isInteger(fps) || fps < 16 || fps > 60) {
      status.dataset.error = '';
      status.textContent = '放映帧率需为 16–60 的整数。';
      return;
    }
    if (!Number.isInteger(rate) || rate < 8000 || rate > 96000) {
      status.dataset.error = '';
      status.textContent = '输出采样率需为 8000–96000 的整数。';
      return;
    }
    submit.disabled = true;
    delete status.dataset.error;
    status.textContent = '正在按放映帧率重排画格边界…';
    try {
      const data = await request(benchURL(applied.reel, 'preview', {
        start: applied.window.start, end: applied.window.end, profile: applied.profile.profile, fps, rate,
      }));
      axis = data;
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      cursor = playhead = undefined;
      const { clock } = axis;
      clockNote.textContent = clock.identity
        ? `与原声一致 · ${compactNumber(clock.samples)} 个采样位置`
        : `${clock.source_fps}→${clock.presentation_fps} 格/秒 · ${compactNumber(clock.samples)} 个采样位置`;
      status.textContent = `窗口 ${applied.window.start}–${applied.window.end - 1} 格 · ${compactNumber(clock.samples)} 个采样位置 · ${compactNumber(clock.duration_ms)} 毫秒 · 可读 ${compactNumber(clock.readable_samples)} 样本 · 转换边缘 ${compactNumber(clock.conversion_edge_samples)} · 缺口 ${compactNumber(clock.missing_samples)} · 卷外 ${compactNumber(clock.outside_samples)}`;
      media.hidden = false;
      media.src = audioURL();
      media.load();
      draw();
      show(clock.first_sample);
    } catch (error) {
      axis = undefined;
      status.dataset.error = '';
      status.textContent = error.message;
      draw();
    } finally {
      submit.disabled = false;
    }
  }

  function draw() {
    const { paint, width, height } = context(canvas);
    if (!axis || !width || !height) return;
    const { clock } = axis;
    const span = clock.samples || 1;
    const x = (local) => local * width / span;
    for (const run of axis.coverage) {
      paint.fillStyle = run.status === 'conversion_edge' ? edgeFill : fills[run.status] ?? '#484741';
      paint.fillRect(x(run.sample_start), 0, Math.max(1, x(run.sample_end) - x(run.sample_start)), height);
    }
    for (const marker of axis.markers.slice(1, -1)) vertical(paint, x(marker.window_sample), height, '#858477', true);
    paint.fillStyle = '#dddacb';
    paint.font = '10px ui-monospace, monospace';
    for (const marker of axis.markers) {
      paint.fillText(String(marker.frame), Math.min(width - 14, Math.max(0, x(marker.window_sample) + 2)), height - 4);
    }
    if (cursor !== undefined) vertical(paint, x(cursor - clock.first_sample + .5), height, '#f6a069');
    if (playhead !== undefined) vertical(paint, x(playhead - clock.first_sample), height, '#fbf5e6', true);
  }

  canvas.addEventListener('click', (event) => {
    if (!axis) return;
    seek(axis.clock.first_sample + Math.min(axis.clock.samples - 1, Math.floor(fraction(event, canvas) * axis.clock.samples)));
  });
  new ResizeObserver(draw).observe(canvas);

  media.addEventListener('loadedmetadata', () => {
    if (axis && cursor !== undefined) media.currentTime = (cursor - axis.clock.first_sample) / axis.clock.rate;
  });
  media.addEventListener('timeupdate', () => {
    if (!axis || media.hidden) return;
    playhead = axis.clock.first_sample + Math.round(media.currentTime * axis.clock.rate);
    draw();
  });

  return {
    setWindow(data) {
      applied = data;
      pending?.abort();
      pending = undefined;
      axis = cursor = playhead = undefined;
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      clockNote.textContent = `原生 ${data.rate} 赫兹 · 放映帧率可另设`;
      status.textContent = '试映会按放映帧率重排当前窗口，原卷采样时钟不变。';
      delete status.dataset.error;
      for (const key of ['sample', 'frame', 'status', 'source', 'pcm']) value(key);
      draw();
    },
    // select mirrors the shared probe onto the preview axis. It only renders a
    // reading once a preview has been loaded for this window.
    select(frame, row) {
      if (!axis) return;
      const picture = axis.frames.find((entry) => entry.frame === frame);
      if (!picture) return;
      const span = picture.sample_end - picture.sample_start;
      const sample = axis.clock.first_sample + picture.sample_start + Math.min(span - 1, Math.floor(row * span / axis.clock.rows_per_frame));
      if (cursor === sample) return;
      if (!media.hidden && media.readyState >= 1) media.currentTime = (sample - axis.clock.first_sample) / axis.clock.rate;
      cursor = sample;
      draw();
      show(sample);
    },
  };
}
