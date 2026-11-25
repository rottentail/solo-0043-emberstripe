import { benchURL, compactNumber, position, request } from './client.js';

const confidenceName = Object.freeze({
  exact: '精确（单一候选）',
  bounded: '有界（候选范围）',
  ambiguous: '周期歧义（多组同分候选）',
  inconclusive: '证据不足',
});

const confidenceTone = Object.freeze({
  exact: 'sync-exact',
  bounded: 'sync-bounded',
  ambiguous: 'sync-ambiguous',
  inconclusive: 'sync-inconclusive',
});

export function synccheck(root, onPosition) {
  const button = root.querySelector('#sync-button');
  const badge = root.querySelector('#sync-badge');
  const status = root.querySelector('#sync-status');
  const panel = root.querySelector('#sync-result');
  const values = root.querySelector('#sync-values');
  const caveats = root.querySelector('#sync-caveats');
  const evidence = root.querySelector('#sync-evidence');
  let data, pending;

  const setValue = (key, text) => {
    values.querySelector(`[data-value="${key}"]`).textContent = text;
  };

  button.addEventListener('click', async () => {
    if (!data || button.disabled) return;
    const active = data;
    const controller = new AbortController();
    pending?.abort();
    pending = controller;
    button.disabled = true;
    badge.textContent = '';
    status.dataset.error = '';
    status.textContent = '正在匹配左右校验序列…';
    try {
      const response = await request(benchURL(active.reel, 'synccheck', active.params), { signal: controller.signal });
      if (active !== data) return;
      render(response.sync);
      status.textContent = '';
    } catch (error) {
      if (pending === controller && !controller.signal.aborted) {
        status.dataset.error = '';
        status.textContent = error.message;
      }
    } finally {
      if (pending === controller) button.disabled = false;
    }
  });

  function lagText(check) {
    if (check.min_lag_rows === check.max_lag_rows) {
      return `${check.best_lag_rows} 行（${compactNumber(check.best_lag_ms)} 毫秒）`;
    }
    return `${check.min_lag_rows}–${check.max_lag_rows} 行（${compactNumber(check.min_lag_ms)}–${compactNumber(check.max_lag_ms)} 毫秒），首选 ${check.best_lag_rows} 行`;
  }

  function render(check) {
    panel.hidden = false;
    badge.textContent = confidenceName[check.confidence] ?? check.confidence;
    badge.className = `sync-badge ${confidenceTone[check.confidence] ?? ''}`;
    setValue('verdict', confidenceName[check.confidence] ?? check.confidence);
    setValue('lag', lagText(check));
    setValue('period', check.repetition_period_rows
      ? `${check.repetition_period_rows} 行`
      : '未检出稳定周期');
    setValue('support', `${compactNumber(check.support_pairs)} 个共同可读位置 · 剔除削顶 ${check.clipped_rows_skipped} 行`);

    caveats.replaceChildren(...check.caveats.map((item) => {
      const paragraph = document.createElement('p');
      paragraph.className = `sync-caveat ${item.code}`;
      paragraph.textContent = item.message;
      return paragraph;
    }));

    const anchors = check.evidence.map((anchor) => {
      const click = document.createElement('button');
      click.type = 'button';
      click.className = 'sync-anchor';
      click.title = '定位到该画格行并联动试听';
      const title = document.createElement('span');
      title.className = 'sync-anchor-position';
      title.textContent = `${position(anchor.picture_row, data.rows_per_frame)} · ${anchor.kind}`;
      const detail = document.createElement('span');
      detail.className = 'sync-anchor-samples';
      detail.textContent = `左 ${anchor.left_sample} / 右 ${anchor.right_sample} · 滞后 ${anchor.lag} 行`;
      click.append(title, detail);
      click.addEventListener('click', () => onPosition(anchor.frame, anchor.row));
      return click;
    });
    evidence.replaceChildren(...anchors);
  }

  return {
    setWindow(windowData) {
      data = windowData;
      pending?.abort();
      pending = undefined;
      button.disabled = false;
      panel.hidden = true;
      badge.textContent = '';
      badge.className = 'sync-badge';
      status.textContent = '';
    },
  };
}
