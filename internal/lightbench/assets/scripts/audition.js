import { captureURL } from './client.js';

export function audition(root, onTime) {
  const button = root.querySelector('#audition-button');
  const media = root.querySelector('#audition-media');
  const message = root.querySelector('#audition-status');
  let data, selected = 0;

  button.addEventListener('click', () => {
    if (!data || !data.live) return;
    const src = captureURL(data.reel, 'audition', {
      start: data.window.start, end: data.window.end, profile: data.profile.profile,
    });
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
    message.textContent = '试听载入失败，请重试。';
    button.disabled = false;
  });
  media.addEventListener('timeupdate', () => {
    if (media.hidden || !data) return;
    onTime(data.window.start * data.rows_per_frame + Math.round(media.currentTime * data.rate));
  });

  return {
    setWindow(windowData) {
      data = windowData;
      selected = 0;
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      button.disabled = !data.live;
      message.textContent = data.live ? '' : '此步骤只能按旧记录查，不能按当前扫描重放试听。';
    },
    select(frame, row) {
      selected = (frame - data.window.start) * data.rows_per_frame + row;
      if (!media.hidden && media.readyState >= 1) media.currentTime = selected / data.rate;
    },
  };
}
