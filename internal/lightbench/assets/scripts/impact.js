const defaultSurvey = {
  original: 'cinder-a',
  rescan: 'vireo-b',
  profile: 'bench',
  references: [
    { id: 'k0', original: { page: 0, row: 1000 }, rescan: { page: 0, row: 960 } },
    { id: 'k2', original: { page: 2, row: 1000 }, rescan: { page: 2, row: 960 } },
  ],
  marks: [
    {
      id: 'torn-across-join',
      note: '横跨接页；若原扫缺页，补扫只负责补扫侧证据',
      start: { page: 1, row: 1900 },
      end: { page: 2, row: 120 },
      x0: 72,
      x1: 82,
    },
    {
      id: 'picture-only',
      page: 1,
      start_row: 400,
      end_row: 500,
      zone: 'picture',
    },
  ],
};

const surveyInput = document.querySelector('#survey');
const status = document.querySelector('#status');
const report = document.querySelector('#report');
surveyInput.value = JSON.stringify(defaultSurvey, null, 2);

function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[ch]));
}

function boundary(value) {
  if (!value) return '—';
  return `p${String(value.page).padStart(4, '0')}#${value.row}（绝对行 ${value.absolute_row}）`;
}

function absRange(range) {
  return range ? `${range.start}–${range.end}（半开）` : '—';
}

function frameRange(range, rows) {
  const startFrame = Math.floor(range.start / rows);
  const startRow = range.start % rows;
  const endFrame = Math.floor(range.end / rows);
  const endRow = range.end % rows;
  return `${startFrame}格#${startRow} → ${endFrame}格#${endRow}；绝对画格行 ${range.start}–${range.end}`;
}

function evidenceList(evidence) {
  if (!evidence?.length) return '<p class="muted">无页证。</p>';
  return `<ul class="evidence">${evidence.map((ev) => `
    <li class="${ev.present ? 'present' : 'absent'}">
      <code>${esc(ev.reel)} · 第 ${ev.page} 页 · 行 ${ev.start_row}:${ev.end_row}</code>
      ${ev.present ? `<span>${ev.depth}-bit · <code>${esc((ev.sha256 || '').slice(0, 12))}</code></span>` : '<strong>无可用页</strong>'}
      ${ev.reason ? `<small>${esc(ev.reason)}</small>` : ''}
      ${ev.proves_original_scan ? '<em>可作原扫证据</em>' : '<em class="lineage-boundary">不证明原扫</em>'}
    </li>`).join('')}</ul>`;
}

function sideHtml(side, reel) {
  if (!side) return '<p class="muted">未能投影：缺少双方认定的参照。</p>';
  const rows = reel.rows_per_frame;
  return `
    <div class="side side-${esc(side.side)}">
      <h4>${side.side === 'original' ? '原扫' : '补扫'} · <code>${esc(side.reel)}</code></h4>
      <p>扫描区间：${boundary(side.start)} 至 ${boundary(side.end)}；列 x=${side.columns.x0}:${side.columns.x1}</p>
      <p><strong>实际可复听：${side.audible_row_count} 行</strong></p>
      <ul>${(side.audible_frame_runs || []).map((r) => `<li>${esc(frameRange(r, rows))}</li>`).join('') || '<li>无可复听行</li>'}</ul>
      ${(side.lane_hits || []).map((lane) => `
        <section class="lane">
          <h5>${lane.lane === 0 ? '左' : '右'}声槽 · ${lane.audible_row_count} 可复听行</h5>
          ${lane.frame_runs.map((run) => `
            <div class="run status-${esc(run.status)}">
              <div>${esc(frameRange({ start: run.frame_row_start, end: run.frame_row_end }, rows))}</div>
              <div>状态 <code>${esc(run.status)}</code>${run.audible ? ' · 可复听' : ''}；声源扫描行 ${run.source_scan_start}–${run.source_scan_end}</div>
              ${run.reason ? `<small>${esc(run.reason)}</small>` : ''}
              <details><summary>回查页证</summary>${evidenceList(run.evidence)}</details>
            </div>`).join('')}
        </section>`).join('') || '<p>未碰到声槽。</p>'}
      <h5>画面受影响范围</h5>
      <ul>${(side.picture_runs || []).map((run) => `<li>${esc(frameRange({ start: run.frame_row_start, end: run.frame_row_end }, rows))} · ${esc(run.status)}</li>`).join('') || '<li>无</li>'}</ul>
      <h5>只有画面受影响</h5>
      <ul>${(side.picture_only_runs || []).map((run) => `<li>${esc(frameRange({ start: run.frame_row_start, end: run.frame_row_end }, rows))} · ${esc(run.status)}</li>`).join('') || '<li>无</li>'}</ul>
      <details><summary>本侧全部页证</summary>${evidenceList(side.evidence)}</details>
    </div>`;
}

function render(data) {
  report.innerHTML = `
    <div class="impact-summary">
      <h2>影响图 <code>${esc(data.schema)}</code></h2>
      <p>原扫 <strong>${esc(data.original_reel.name)}</strong>（偏移 ${data.original_reel.lead_rows}）；
         补扫 <strong>${esc(data.rescan_reel.name)}</strong>（偏移 ${data.rescan_reel.lead_rows}）。</p>
      <ul>${data.lineage_policy.map((line) => `<li>${esc(line)}</li>`).join('')}</ul>
    </div>
    ${data.segments.map((seg) => `
      <article class="segment-card">
        <h3>${esc(seg.id)}</h3>
        ${seg.note ? `<p>${esc(seg.note)}</p>` : ''}
        <p>已证实原扫区间：${(seg.corroborated_original_ranges || []).map(absRange).map(esc).join('；') || '无'}</p>
        ${seg.confirm_original_from ? `<p class="cutoff">从 ${esc(boundary(seg.confirm_original_from))} 起，后段必须回原扫确认。</p>` : ''}
        ${seg.confirm_original_before ? `<p class="cutoff">直到 ${esc(boundary(seg.confirm_original_before))} 的首段未证实。</p>` : ''}
        <div class="side-grid">
          ${sideHtml(seg.original, data.original_reel)}
          ${sideHtml(seg.rescan, data.rescan_reel)}
        </div>
        ${seg.follow_ups?.length ? `<section class="followups"><h4>待确认</h4><ul>${seg.follow_ups.map((f) => `
          <li><code>${esc(f.reel)}</code> ${esc(f.kind)}：${esc(boundary(f.from))}–${esc(boundary(f.to))}<br><small>${esc(f.reason)}</small></li>`).join('')}</ul></section>` : ''}
      </article>`).join('')}
    <details class="raw-json"><summary>原始 JSON</summary><pre>${esc(JSON.stringify(data, null, 2))}</pre></details>`;
}

async function submit(format = '') {
  status.textContent = '计算中…';
  let payload;
  try {
    JSON.parse(surveyInput.value);
    const response = await fetch(`/impact-map${format ? `?format=${format}` : ''}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: surveyInput.value,
    });
    if (!response.ok) {
      const err = await response.json().catch(() => ({ error: response.statusText }));
      throw new Error(err.error || response.statusText);
    }
    if (format === 'markdown') {
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'impact-map.md';
      a.click();
      URL.revokeObjectURL(url);
      status.textContent = 'Markdown 已下载。';
      return;
    }
    payload = await response.json();
    render(payload);
    status.textContent = '已生成。';
  } catch (error) {
    status.textContent = `失败：${error.message}`;
  }
}

document.querySelector('#generate').addEventListener('click', () => submit());
document.querySelector('#markdown').addEventListener('click', () => submit('markdown'));
