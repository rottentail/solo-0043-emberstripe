import { captureURL } from './client.js';

export function audition(root, onTime) {
  const button = root.querySelector('#audition-button');
  const media = root.querySelector('#audition-media');
  const message = root.querySelector('#audition-status');
  let data, selected = 0, range;

  const origin = () => data.window.start * data.rows_per_frame;
  const span = () => (data.window.end - data.window.start) * data.rows_per_frame;
  const bounded = () => range && (range.start > origin() || range.end < origin() + span());
  const rangeStart = () => (range.start - origin()) / data.rate;

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
    media.currentTime = bounded() ? rangeStart() : selected / data.rate;
    message.textContent = bounded() ? '播放限制在复听范围内' : '可播放或拖动定位';
  });
  media.addEventListener('error', () => {
    if (media.hidden) return;
    message.textContent = '试听载入失败，请重试。';
    button.disabled = false;
  });
  media.addEventListener('play', () => {
    if (!data || !bounded()) return;
    const absolute = origin() + media.currentTime * data.rate;
    if (absolute < range.start || absolute >= range.end) media.currentTime = rangeStart();
  });
  media.addEventListener('timeupdate', () => {
    if (media.hidden || !data) return;
    const absolute = origin() + Math.round(media.currentTime * data.rate);
    if (bounded() && absolute >= range.end) {
      media.pause();
      media.currentTime = rangeStart();
      return;
    }
    onTime(absolute);
  });

  return {
    setWindow(windowData) {
      data = windowData;
      selected = 0;
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
    setRange(start, end) {
      range = { start, end };
      if (!data || media.hidden || media.readyState < 1 || media.paused || !bounded()) return;
      if (origin() + media.currentTime * data.rate >= range.end) {
        media.pause();
        media.currentTime = rangeStart();
      }
    },
  };
}
