import { captureURL, request } from './client.js';

const outcomeName = Object.freeze({
  required: '必听·独立播放',
  shared: '共用一次播放',
  added: '选听·新增播放',
  split: '选听·已拆开缩短',
  omitted: '选听·已省去',
});
const sideName = Object.freeze({ lead: '前上下文', tail: '后上下文', anchor: '锚点本身' });

export function reviewBoard(root) {
  const form = root.querySelector('#board-brief');
  const status = root.querySelector('#board-status');
  const result = root.querySelector('#board-result');
  const listen = root.querySelector('#board-listen');
  const audio = root.querySelector('#board-audio');
  const summary = root.querySelector('#board-summary');
  const itemBody = root.querySelector('#board-items');
  const adjustmentBox = root.querySelector('#board-adjustments');
  let plan;

  form.querySelectorAll('.board-add').forEach((button) => {
    button.addEventListener('click', () => addRow(button.dataset.kind));
  });

  function addRow(kind) {
    const container = form.querySelector(`.board-rows[data-kind="${kind}"]`);
    const template = container.querySelector('.board-row').cloneNode(true);
    template.querySelectorAll('input').forEach((input) => {
      if (!/lead|tail|priority/.test(input.name)) input.value = '';
    });
    container.append(template);
    syncRemoveButtons();
  }

  function syncRemoveButtons() {
    form.querySelectorAll('.board-rows').forEach((container) => {
      const rows = container.querySelectorAll('.board-row');
      rows.forEach((row) => {
        const button = row.querySelector('.board-remove');
        button.hidden = rows.length === 1;
        button.onclick = () => {
          row.remove();
          syncRemoveButtons();
        };
      });
    });
  }
  syncRemoveButtons();

  function collect(kind) {
    const prefix = kind === 'required' ? 'r-' : 'o-';
    return [...form.querySelectorAll(`.board-rows[data-kind="${kind}"] .board-row`)].map((row, index) => {
      const value = (name) => row.querySelector(`[name="${prefix}${name}"]`).value.trim();
      const integer = (name) => Number.parseInt(value(name), 10) || 0;
      const item = {
        id: value('id') || `${kind}-${index + 1}`,
        frame: integer('frame'), row: integer('row'),
        lead_rows: integer('lead'), tail_rows: integer('tail'),
      };
      if (kind === 'required') {
        const separate = value('sep');
        if (separate) item.separate_from = separate;
      } else {
        item.priority = integer('priority') || index + 1;
      }
      return item;
    });
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    status.removeAttribute('data-error');
    status.textContent = '正在规划听审拼板…';
    listen.disabled = true;
    const brief = {
      reel: root.dataset.reel,
      profile: form.closest('main').querySelector('#window-form select')?.value || '',
      max_seconds: Number.parseFloat(form.querySelector('[name="max_seconds"]').value) || 12,
      required: collect('required'),
      optional: collect('optional'),
    };
    try {
      plan = await request(captureURL(root.dataset.reel, 'review-board', {}), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(brief),
      });
      renderPlan(plan);
      status.textContent = plan.feasible
        ? `拼板可行：${plan.used_seconds.toFixed(3)} / ${plan.max_seconds} 秒，共 ${plan.clips.length} 次播放。`
        : '必听证据与时长冲突，已给出具体到位置的调整清单。';
      if (!plan.feasible) status.setAttribute('data-error', '');
      listen.disabled = !plan.feasible;
    } catch (error) {
      plan = undefined;
      result.hidden = true;
      status.setAttribute('data-error', '');
      status.textContent = `规划失败。${error.message}`;
    }
  });

  listen.addEventListener('click', async () => {
    if (!plan) return;
    listen.disabled = true;
    status.textContent = '正在生成可播放 WAV…';
    try {
      const response = await fetch(captureURL(root.dataset.reel, 'review-board/listen', {}), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(plan),
      });
      if (!response.ok) throw new Error('拼板渲染被拒绝，请按调整清单修改后重试。');
      const blob = await response.blob();
      audio.src = URL.createObjectURL(blob);
      audio.play().catch(() => undefined);
      status.textContent = `已载入 ${response.headers.get('X-Board-Seconds') ?? plan.used_seconds.toFixed(3)} 秒拼板，可直接播放或下载。`;
    } catch (error) {
      status.setAttribute('data-error', '');
      status.textContent = error.message;
    } finally {
      listen.disabled = false;
    }
  });

  function rowLabel(rowsPerFrame, absolute) {
    return `${Math.floor(absolute / rowsPerFrame)} 格 ${absolute % rowsPerFrame} 行`;
  }

  function renderPlan(next) {
    result.hidden = false;
    const clipMap = new Map(next.clips.map((clip) => [clip.id, clip]));
    summary.innerHTML = '';
    const facts = [
      `卷 ${next.reel} · 标定 ${next.profile}`,
      `${next.sample_rate} 赫兹 · 每格 ${next.rows_per_frame} 行`,
      `预算 ${next.max_seconds} 秒（${next.budget_samples} 帧）`,
      `实际 ${next.used_seconds.toFixed(3)} 秒（${next.used_samples} 帧）`,
    ];
    facts.forEach((fact) => {
      const span = document.createElement('span');
      span.className = 'board-fact';
      span.textContent = fact;
      summary.append(span);
    });

    itemBody.innerHTML = '';
    next.items.forEach((item, index) => {
      const clip = clipMap.get(item.clip_ids[0]);
      const tr = document.createElement('tr');
      const limits = (item.limits || []).map((note) =>
        `${sideName[note.side] || note.side}：${note.reason}（行 ${note.limited_to.start}–${note.limited_to.end - 1}）`).join('；');
      const shared = item.shared_with?.length ? ` 与 ${item.shared_with.join('、')} 共用` : '';
      tr.innerHTML = `
        <td>${index + 1}</td>
        <td><strong>${item.id}</strong><br><small>${item.frame} 格 ${item.row} 行${item.note ? ` · ${item.note}` : ''}</small></td>
        <td>${outcomeName[item.outcome] || item.outcome}${shared}${item.reason ? `<br><small>${item.reason}</small>` : ''}</td>
        <td>${item.clip_ids.join('、') || '—'}</td>
        <td>${rowLabel(next.rows_per_frame, item.played.start)} – ${rowLabel(next.rows_per_frame, item.played.end - 1)}</td>
        <td>${clip ? clip.board_offset_seconds.toFixed(3) : '—'}</td>
        <td>${limits || '全部可读'}</td>`;
      itemBody.append(tr);
    });

    adjustmentBox.hidden = next.feasible;
    if (!next.feasible) {
      adjustmentBox.innerHTML = '<h3>必听证据与时长冲突：调整清单</h3>';
      const list = document.createElement('ol');
      next.adjustments.forEach((adjustment) => {
        const li = document.createElement('li');
        li.textContent = `${adjustment.relaxation} — ${adjustment.detail}` +
          (adjustment.gain_seconds ? ` 预计释放 ${adjustment.gain_seconds.toFixed(3)} 秒` : '');
        list.append(li);
      });
      adjustmentBox.append(list);
    }
  }

  return { setWindow() { /* board briefs are independent of the frame window */ } };
}
