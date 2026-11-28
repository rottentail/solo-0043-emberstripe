import { captureURL, compactNumber, request } from './client.js';

export function comparison(root, onPair) {
  const form = root.querySelector('#compare-form');
  const output = root.querySelector('#compare-result');
  const label = root.querySelector('#active-profile');
  const readings = root.querySelector('#calibration-readings');
  const button = form?.querySelector('button');
  let current, pending;

  form?.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!current || button.disabled) return;
    const active = current;
    const controller = new AbortController();
    pending = controller;
    button.disabled = true;
    output.textContent = '正在对照两个标定…';
    try {
      const against = form.elements.against.value;
      const result = await request(captureURL(active.reel, 'compare', { ...active.params, against }), { signal: controller.signal });
      if (active !== current) return;
      const { difference } = result;
      const lines = [
        `${result.left.profile} / ${result.right.profile} · ${compactNumber(difference.compared_frames)} 个共同可读位置`,
        `变化样本 ${difference.changed_samples.map(compactNumber).join(' / ')}（左／右）`,
        `峰值差 ${difference.peak_delta.join(' / ')} · 均方根差 ${difference.rms_delta.map(compactNumber).join(' / ')}`,
      ];
      output.replaceChildren(...lines.map((line) => {
        const paragraph = document.createElement('p');
        paragraph.textContent = line;
        return paragraph;
      }));
      // Only an actual successful two-profile compare becomes the saved pairing;
      // probe checks in the recovery panel never reach here.
      onPair?.(active, against);
    } catch (error) {
      if (!controller.signal.aborted) output.textContent = error.message;
    } finally {
      if (pending === controller) button.disabled = false;
    }
  });

  return {
    setWindow(data) {
      current = data;
      pending?.abort();
      pending = undefined;
      label.textContent = data.profile.profile;
      for (const [index, lane] of ['left', 'right'].entries()) {
        const { dark, clear } = data.profile.lanes[index];
        readings.querySelector(`[data-value="${lane}"]`).textContent = `${compactNumber(dark)} / ${compactNumber(clear)}`;
      }
      if (!form) return;
      const select = form.elements.against;
      for (const option of select.options) option.disabled = option.value === data.profile.profile;
      if (select.options[select.selectedIndex]?.disabled) select.value = [...select.options].find((option) => !option.disabled).value;
      output.textContent = '';
      button.disabled = false;
    },
  };
}
