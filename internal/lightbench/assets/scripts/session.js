import { benchURL, request } from './client.js';

export function windowController(root, onWindow) {
  const reel = root.dataset.reel;
  const form = root.querySelector('#window-form');
  const message = root.querySelector('#window-status');
  const submit = form.querySelector('button');
  let current;

  async function update(event) {
    event?.preventDefault();
    if (submit.disabled) return;
    const params = Object.fromEntries(new FormData(form));
    submit.disabled = true;
    delete message.dataset.error;
    message.textContent = '正在读取声槽与扫描页…';
    try {
      const data = await request(benchURL(reel, 'window', params));
      current = { ...data, reel, params };
      message.textContent = `当前窗口 ${data.window.start}–${data.window.end - 1} 格 · 标定 ${data.profile.profile}`;
      onWindow(current);
    } catch (error) {
      const previous = current ? ` 仍在查看 ${current.window.start}–${current.window.end - 1} 格，标定 ${current.profile.profile}。` : '';
      message.dataset.error = '';
      message.textContent = error.message + previous;
    } finally {
      submit.disabled = false;
    }
  }

  // Deep links from line-audit conclusions carry frame/row and often an
  // explicit review window. Fill the form before the initial request.
  const deep = new URLSearchParams(window.location.search);
  if (deep.get('start')) form.elements.start.value = deep.get('start');
  if (deep.get('end')) form.elements.end.value = deep.get('end');
  if (deep.get('profile')) form.elements.profile.value = deep.get('profile');
  if (!deep.get('start') && deep.get('frame')) {
    const frame = Number(deep.get('frame'));
    const maxEnd = Number(form.elements.end.max || 1);
    form.elements.start.value = Math.max(0, frame - 2);
    form.elements.end.value = Math.min(maxEnd, frame + 3);
  }

  form.addEventListener('submit', update);
  return { start: update };
}
