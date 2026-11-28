import { benchURL, request } from './client.js';

// windowController owns the single applied step. A window request either lands
// a new valid step or reports the error while the previous step stays in
// force, matching what the server still holds.
export function windowController(root, onWindow, probeNow) {
  const reel = root.dataset.reel;
  const form = root.querySelector('#window-form');
  const message = root.querySelector('#window-status');
  const submit = form.querySelector('button');
  let current;

  function adopt(data) {
    current = { ...data, reel };
    const band = data.live ? '' : ' · 仅按旧记录查';
    message.textContent = `当前窗口 ${data.window.start}–${data.window.end - 1} 格 · 标定 ${data.profile.profile}${band}`;
    message.toggleAttribute('data-record', !data.live);
    onWindow(current);
  }

  async function update(event) {
    event?.preventDefault();
    if (submit.disabled) return;
    const params = Object.fromEntries(new FormData(form));
    // The probe selected in the browser is what the step records, so the lead
    // reviewing later sees the same witness the operator was using.
    if (probeNow) Object.assign(params, probeNow());
    submit.disabled = true;
    delete message.dataset.error;
    message.textContent = '正在读取声槽与扫描页…';
    try {
      adopt(await request(benchURL(reel, 'window', params)));
    } catch (error) {
      const previous = current ? ` 仍在查看 ${current.window.start}–${current.window.end - 1} 格，标定 ${current.profile.profile}。` : '';
      message.dataset.error = '';
      message.textContent = error.message + previous;
    } finally {
      submit.disabled = false;
    }
  }

  form.addEventListener('submit', update);

  return {
    async start() {
      try {
        const restored = await request(benchURL(reel, 'applied', {}));
        if (restored.applied !== false) {
          adopt(restored);
          return;
        }
      } catch {
        // Fall through to the default first window.
      }
      await update();
    },
    adopt,
  };
}
