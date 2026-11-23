import { captureURL, compactNumber, request, statusName } from './client.js';

export function probe(root, onPosition) {
  const form = root.querySelector('#probe-form');
  const badge = root.querySelector('#probe-badge');
  const readings = root.querySelector('#probe-values');
  let data, pending;

  function value(key, text = '—') {
    readings.querySelector(`[data-value="${key}"]`).textContent = text;
  }
  function clear() {
    for (const key of ['position', 'source', 'gray', 'sample', 'clipped']) value(key);
  }

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (!data) return;
    onPosition(form.elements.frame.valueAsNumber, form.elements.row.valueAsNumber);
  });

  return {
    setWindow(windowData) {
      data = windowData;
      form.elements.frame.min = data.window.start;
      form.elements.frame.max = data.window.end - 1;
      form.elements.row.max = data.rows_per_frame - 1;
      form.querySelector('button').disabled = false;
    },
    async select(frame, row) {
      pending?.abort();
      const controller = new AbortController();
      pending = controller;
      form.elements.frame.value = frame;
      form.elements.row.value = row;
      clear();
      badge.textContent = '读取中';
      try {
        const result = await request(captureURL(data.reel, 'trace', { frame, row, profile: data.profile.profile }), { signal: controller.signal });
        if (pending !== controller) return;
        badge.textContent = statusName[result.status];
        const absolute = frame * data.rows_per_frame + row;
        value('position', `${absolute} 行 · ${compactNumber(absolute * 1000 / data.rate)} 毫秒`);
        value('source', result.source ? `${result.source.frame} 格 ${result.source.row} 行` : '卷界外');
        if (result.status === 'readable') {
          value('gray', result.gray.map(compactNumber).join(' / '));
          value('sample', result.sample.join(' / '));
          value('clipped', result.clipped.map((flag) => flag ? '是' : '否').join(' / '));
        } else {
          value('sample', '0 / 0（补零）');
        }
      } catch (error) {
        if (pending === controller && !controller.signal.aborted) badge.textContent = error.message;
      }
    },
  };
}
