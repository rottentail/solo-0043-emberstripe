import { captureURL, compactNumber, request, statusName } from './client.js';

export function probe(root, onPosition, onRelocate) {
  const form = root.querySelector('#probe-form');
  const badge = root.querySelector('#probe-badge');
  const readings = root.querySelector('#probe-values');
  const carry = root.querySelector('#probe-carry');
  const carryNote = root.querySelector('#probe-carry-note');
  const carryButton = root.querySelector('#probe-relocate');
  let data, pending, target;

  function value(key, text = '—') {
    readings.querySelector(`[data-value="${key}"]`).textContent = text;
  }
  function clear() {
    for (const key of ['position', 'source', 'gray', 'sample', 'clipped']) value(key);
  }
  function dropCarry() {
    target = undefined;
    carry.hidden = true;
    carryButton.hidden = true;
  }

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (!data) return;
    onPosition(form.elements.frame.valueAsNumber, form.elements.row.valueAsNumber);
  });
  carryButton.addEventListener('click', () => {
    if (target) onRelocate(target.frame, target.row);
  });

  return {
    setWindow(windowData) {
      data = windowData;
      form.elements.frame.min = data.window.start;
      form.elements.frame.max = data.window.end - 1;
      form.elements.row.max = data.rows_per_frame - 1;
      form.querySelector('button[type="submit"]').disabled = false;
      dropCarry();
    },
    // carry explains why the previous probe row did not follow the window and
    // offers one action to move to the closest visible picture row.
    carry(plan, landing) {
      dropCarry();
      if (!plan?.lostSelection) return;
      const lost = `${plan.lostSelection.frame} 格 ${plan.lostSelection.row} 行`;
      let text = `原探针位置 ${lost} 不在新窗口，现为 ${landing.frame} 格 ${landing.row} 行。`;
      if (plan.nearestSelection) {
        target = plan.nearestSelection;
        carryButton.textContent = `定位到最近的 ${target.frame} 格 ${target.row} 行`;
        carryButton.hidden = false;
      }
      carry.hidden = false;
      carryNote.textContent = text;
    },
    async select(frame, row) {
      dropCarry();
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
