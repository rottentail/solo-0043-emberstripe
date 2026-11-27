import { captureURL } from './client.js';

export function audition(root, onTime, onPlayed) {
  const button = root.querySelector('#audition-button');
  const media = root.querySelector('#audition-media');
  const message = root.querySelector('#audition-status');
  let data, selected = 0;
  let live;

  const firstRow = () => data.window.start * data.rows_per_frame;
  const rowAt = (time) => firstRow() + Math.round(time * data.rate);
  const lastRow = () => data.window.end * data.rows_per_frame;

  // A live segment only exists while the media clock advances under normal
  // playback. Seeking and buffering stalls close it first, so scrubbing the
  // playhead or waiting on the browser never gets counted as heard material.
  function closeLive(time) {
    if (!live) return;
    const end = Math.min(lastRow(), rowAt(time ?? media.currentTime) + 1);
    const start = Math.max(firstRow(), live.start);
    live = undefined;
    if (end > start) onPlayed({ start, end });
  }

  button.addEventListener('click', () => {
    if (!data) return;
    const src = captureURL(data.reel, 'audition', data.params);
    media.hidden = false;
    message.textContent = '正在载入当前区间…';
    button.disabled = true;
    media.src = src;
    media.load();
  });
  media.addEventListener('loadedmetadata', () => {
    media.currentTime = selected / data.rate;
    message.textContent = '可播放或拖动定位';
  });
  media.addEventListener('error', () => {
    if (media.hidden) return;
    live = undefined;
    message.textContent = '试听载入失败，请重试。';
    button.disabled = false;
  });
  media.addEventListener('seeking', () => closeLive());
  media.addEventListener('waiting', () => closeLive());
  media.addEventListener('stalled', () => closeLive());
  media.addEventListener('pause', () => closeLive());
  media.addEventListener('ended', () => closeLive(media.duration));
  media.addEventListener('playing', () => {
    if (media.hidden || !data || media.playbackRate !== 1) return;
    closeLive();
    live = { start: rowAt(media.currentTime) };
  });
  media.addEventListener('timeupdate', () => {
    if (media.hidden || !data) return;
    onTime(rowAt(media.currentTime));
    // Emit the growing live extent so the review panel updates during playback;
    // overlapping extents merge into a single heard range.
    if (live) onPlayed({ start: live.start, end: Math.min(lastRow(), rowAt(media.currentTime) + 1) });
  });

  return {
    setWindow(windowData) {
      data = windowData;
      selected = 0;
      live = undefined;
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      button.disabled = false;
      message.textContent = '';
    },
    select(frame, row) {
      selected = (frame - data.window.start) * data.rows_per_frame + row;
      if (!media.hidden && media.readyState >= 1) media.currentTime = selected / data.rate;
    },
  };
}
