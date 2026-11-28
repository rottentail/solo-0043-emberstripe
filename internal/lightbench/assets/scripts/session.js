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

  form.addEventListener('submit', update);

  // Unblind results link back with ?start=&end=&profile=; prefill so the
  // reviewer lands on the exact disputed picture window.
  for (const [name, input] of [['start', form.elements.start], ['end', form.elements.end], ['profile', form.elements.profile]]) {
    const value = new URLSearchParams(location.search).get(name);
    if (value !== null && input && (input.tagName !== 'SELECT' || Array.from(input.options).some((option) => option.value === value))) {
      input.value = value;
    }
  }
  return { start: update };
}
