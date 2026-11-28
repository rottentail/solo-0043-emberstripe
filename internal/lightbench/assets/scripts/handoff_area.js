import { request } from './client.js';

const verdictMeta = Object.freeze({
  audible: { label: '确有声音', className: 'verdict-audible' },
  time_range: { label: '只有时间范围', className: 'verdict-time' },
  unverifiable: { label: '没有足够材料核实', className: 'verdict-none' },
});

const statusName = Object.freeze({
  readable: '可读',
  missing_sheet: '声槽缺页（补零）',
  outside_capture: '移出卷界（补零）',
});

export function handoffArea(root) {
  const form = root.querySelector('#handoff-search-form');
  const status = root.querySelector('#handoff-search-status');
  const results = root.querySelector('#handoff-results');
  const next = root.querySelector('#handoff-next');
  const note = root.querySelector('#handoff-page-note');
  let snapshot = '';
  let lastQuery = '';

  const qs = (data) => new URLSearchParams(data).toString();

  async function runSearch(params, useSnapshot) {
    const url = `/handoff/parcels?${qs(params)}${useSnapshot && snapshot ? `&snapshot=${encodeURIComponent(snapshot)}` : ''}`;
    const page = await request(url);
    if (!useSnapshot) snapshot = page.snapshot_token;
    render(page, params);
    return page;
  }

  function verdictBadge(verdict) {
    const meta = verdictMeta[verdict] || { label: verdict, className: '' };
    return `<span class="verdict-badge ${meta.className}">${meta.label}</span>`;
  }

  function render(page, params) {
    results.innerHTML = '';
    if (page.items.length === 0) {
      status.textContent = '没有通过完整性校验的交接包覆盖该地址。';
    } else {
      status.textContent = `本屏 ${page.items.length} 份，已按确有声音 → 仅时间范围 → 无法核实排序。`;
    }
    for (const item of page.items) {
      const f = item.finding;
      const card = document.createElement('article');
      card.className = 'handoff-card';
      const locateURL = `/handoff/parcels/${encodeURIComponent(f.parcel_id)}/locate?${qs(params)}`;
      const listenURL = `/handoff/parcels/${encodeURIComponent(f.parcel_id)}/audition?${qs(params)}`;
      const sampleLine = f.sample_frame === undefined ? '' :
        `<dd>包内采样帧</dd><dt>${f.sample_frame}（${f.sample_rate} 赫兹）</dt>`;
      const coverLine = f.coverage_status ? `${statusName[f.coverage_status] || f.coverage_status}` : '—';
      const pcmLine = f.pcm_nonzero === undefined ? '' :
        (f.pcm_nonzero ? '该帧 PCM 非零' : '该帧 PCM 为零（占位）');
      card.innerHTML = `
        <header>${verdictBadge(f.verdict)}<strong class="handoff-capture">${f.capture}</strong></header>
        <dl class="handoff-facts">
          <dt>包标识</dt><dd class="handoff-id">${f.parcel_id}</dd>
          <dt>包时钟</dt><dd>${f.rows_per_frame} 行/格 · 偏移 ${f.lead_rows >= 0 ? '+' : ''}${f.lead_rows} 行</dd>
          <dt>报告地址</dt><dd>${f.address_mode === 'picture' ? '画格' : '声槽页'} ${f.query_frame} · 行 ${f.query_row}</dd>
          <dt>折算画格行</dt><dd>${f.picture_row}${f.inside_window ? '' : '（窗外）'}</dd>
          <dt>源声槽位置</dt><dd>${f.source ? `${f.source.frame} 页 ${f.source.row} 行` : '无'}</dd>
          <dt>覆盖说明</dt><dd>${coverLine}</dd>
          ${sampleLine ? `<dt>采样位置</dt><dd>${f.sample_frame}</dd>` : ''}
          <dt>画面页</dt><dd>${f.picture_in_parcel ? (f.has_picture_sheet ? '包内有该页画面摘要' : '包内无该页画面，但声槽另行成立') : '不在包画格窗内'}</dd>
          ${pcmLine ? `<dt>PCM 核验</dt><dd>${pcmLine}</dd>` : ''}
        </dl>
        <div class="handoff-card-actions">
          <button type="button" data-locate="${locateURL}">重新核验此包</button>
          <a class="button secondary" data-audition="${listenURL}" href="${listenURL}" target="_blank" rel="noopener">复听 WAV</a>
        </div>
        <p class="handoff-card-status" role="status"></p>`;
      results.appendChild(card);
    }
    next.hidden = page.exhausted || !page.next_token;
    note.textContent = page.truncated
      ? `本屏只展开了部分包，点翻页继续；快照剩余 ${page.totals.remaining_in_snapshot} 份。`
      : `已扫描 ${page.totals.scanned} 份 · 无法通过完整性校验 ${page.totals.failed_integrity} 份 · 快照剩余 ${page.totals.remaining_in_snapshot} 份。`;
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(form).entries());
    data.frame = String(Number(data.frame));
    data.row = String(Number(data.row));
    if (!data.capture) delete data.capture;
    lastQuery = qs(data);
    status.textContent = '正在按各包自己的时钟核验交接包…';
    try {
      await runSearch(data, false);
    } catch (error) {
      status.textContent = `查找失败。${error.message}`;
      results.innerHTML = '';
      next.hidden = true;
    }
  });

  next.addEventListener('click', async () => {
    if (!snapshot) return;
    const data = Object.fromEntries(new FormData(form).entries());
    next.disabled = true;
    try {
      await runSearch(data, true);
    } catch (error) {
      status.textContent = `快照已失效，请重新查找。${error.message}`;
      next.hidden = true;
      snapshot = '';
    } finally {
      next.disabled = false;
    }
  });

  results.addEventListener('click', async (event) => {
    const locateBtn = event.target.closest('[data-locate]');
    if (!locateBtn) return;
    const card = locateBtn.closest('.handoff-card');
    const cardStatus = card.querySelector('.handoff-card-status');
    cardStatus.textContent = '正在按当前磁盘字节重新校验（不沿用列表结论）…';
    try {
      const item = await request(locateBtn.dataset.locate);
      const f = item.finding;
      cardStatus.textContent = `复核完成：${verdictMeta[f.verdict].label}` +
        (f.pcm_nonzero !== undefined ? ` · ${f.pcm_nonzero ? 'PCM 非零' : 'PCM 为零'}` : '');
    } catch (error) {
      cardStatus.textContent = `复核失败，文件可能已被替换。${error.message}`;
    }
  });

  // Keep a stable query string for external callers/tests.
  return { lastQuery: () => lastQuery };
}

const handoffRoot = document.querySelector('.handoff-area');
if (handoffRoot) {
  handoffArea(handoffRoot);
}
