import { request } from './client.js';
import { context, fraction, vertical } from './canvas.js';

const laneName = ['左声槽', '右声槽'];
const statusName = {
  precise: '行级证据较准',
  range_only: '只能确定范围',
  no_evidence: '证据不足',
  missing_page: '被缺页阻断',
  conflicting: '声槽证据冲突',
};

function el(tag, props = {}, children = []) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === 'text') node.textContent = value;
    else if (key === 'className') node.className = value;
    else if (key.startsWith('on') && typeof value === 'function') node.addEventListener(key.slice(2), value);
    else if (value !== undefined && value !== null && value !== false) node.setAttribute(key, value);
  }
  for (const child of [].concat(children)) {
    if (child === null || child === undefined) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return node;
}

function intervalText(interval) {
  return interval ? `${interval.start}–${interval.end} 行（半开）` : '—';
}

function percent(value) {
  return `${Math.round((value ?? 0) * 100)}%`;
}

export function pairPage(root) {
  const form = root.querySelector('#pair-form');
  const status = root.querySelector('#pair-status');
  const summary = root.querySelector('#pair-summary');
  const list = root.querySelector('#pair-findings');
  const viewer = root.querySelector('#source-viewer');
  const sourceTitle = root.querySelector('#source-title');
  const sourceImage = root.querySelector('#source-image');
  const sourceMarker = root.querySelector('#source-marker');
  const sourceCaption = root.querySelector('#source-caption');
  const sourceWorkbench = root.querySelector('#source-workbench');
  let report;
  let source = null;

  function drawMarker() {
    if (!source) return;
    const { paint, width, height } = context(sourceMarker);
    paint.clearRect(0, 0, width, height);
    vertical(paint, Math.max(1, (source.row + .5) * width / source.rows), height, '#f6a069');
  }

  function showSource(side, frame, row, workbenchURL) {
    const info = report[side.toLowerCase()];
    source = { side, reel: info.name, frame, row, rows: info.rows_per_frame, workbenchURL };
    viewer.hidden = false;
    sourceTitle.textContent = `${side} · ${info.name} · 第 ${frame} 页`;
    sourceCaption.textContent = `原页第 ${row} 行；横向为页内行号投影，橙线为结论起点。点击投影可改查该行。`;
    sourceWorkbench.href = workbenchURL || '#';
    sourceWorkbench.toggleAttribute('hidden', !workbenchURL);
    sourceImage.hidden = false;
    sourceImage.style.display = '';
    sourceImage.alt = `${info.name} 第 ${frame} 页扫描投影`;
    sourceImage.onload = drawMarker;
    sourceImage.onerror = () => {
      sourceImage.hidden = true;
      sourceCaption.textContent = `第 ${frame} 页没有扫描原页（接口返回 404）；这是实物缺页，不是行距证据。`;
    };
    sourceImage.src = `/lightbench/${encodeURIComponent(info.name)}/strip?frame=${frame}`;
    drawMarker();
    viewer.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  sourceMarker.addEventListener('click', (event) => {
    if (!source) return;
    source.row = Math.min(source.rows - 1, Math.floor(fraction(event, sourceMarker) * source.rows));
    sourceCaption.textContent = `原页第 ${source.frame} 页 · 第 ${source.row} 行（点击投影可继续移动）。`;
    if (source.workbenchURL) {
      try {
        const url = new URL(source.workbenchURL, window.location.origin);
        url.searchParams.set('row', source.row);
        source.workbenchURL = url.pathname + url.search;
        sourceWorkbench.href = source.workbenchURL;
      } catch { /* keep existing link */ }
    }
    drawMarker();
  });
  new ResizeObserver(drawMarker).observe(sourceMarker);

  function coordinateCard(side, finding) {
    const prefix = side === 'A' ? 'a' : 'b';
    const interval = finding[`${prefix}_interval`] || finding[`range_${prefix}`];
    const address = finding[`address_${prefix}`];
    const links = finding[`links_${prefix}`] || {};
    const info = report[prefix];
    const safeStart = Math.min(info.total_rows - 1, Math.max(0, interval.start));
    const frame = address?.valid ? address.frame : Math.floor(safeStart / info.rows_per_frame);
    const row = address?.valid ? address.row_in_frame : safeStart % info.rows_per_frame;
    const workbench = links.workbench_url || address?.workbench_url;
    const openOriginal = el('button', {
      type: 'button', className: 'secondary',
      onClick: () => showSource(side, frame, row, workbench),
    }, `点查 ${side} 原页 ${frame}:${row}`);
    const openBench = workbench ? el('a', {
      className: 'button secondary', href: workbench, target: '_blank', rel: 'noopener',
    }, '单卷工作台') : null;
    const sheetAvailable = !address?.valid || address.sheet_available;
    const openSheet = sheetAvailable ? el('a', {
      className: 'button secondary',
      href: `/lightbench/${encodeURIComponent(info.name)}/sheet?frame=${frame}`,
      target: '_blank', rel: 'noopener',
    }, '完整原页') : null;
    const audio = links.audition_url ? el('audio', { controls: '', preload: 'none', src: links.audition_url }) : null;
    return el('div', { className: 'side-coordinate' }, [
      el('h4', { text: `${side} · ${info.name}` }),
      el('div', { className: 'coordinate-line', text: `对拍区间 ${intervalText(interval)}` }),
      el('div', { className: 'coordinate-line', text: address?.valid ? `源页 ${address.frame} 格 ${address.row_in_frame} 行 · ${address.sheet_available ? '原页存在' : '原页缺失'}` : '行位超出卷界；请用相邻结论点查' }),
      el('div', { className: 'card-actions' }, [openOriginal, openSheet, openBench]),
      audio ? el('div', { className: 'audio-pair' }, [el('label', {}, [`${side} 复听（前后留上下文）`, audio])]) : null,
    ]);
  }

  function evidenceList(finding) {
    if (!finding.evidence?.length) return null;
    return el('ul', { className: 'evidence-list' }, finding.evidence.map((item) => {
      const precision = item.precision_rows ? `；可行边界宽 ${item.precision_rows} 行` : '';
      const delta = item.delta_rows ? `；Δ=${item.delta_rows > 0 ? '+' : ''}${item.delta_rows}` : '';
      return el('li', { text: `${laneName[item.lane] || `声槽 ${item.lane}`}：${statusName[item.status] || item.status}${delta}${precision}。${item.note || ''}` });
    }));
  }

  function findingCard(finding) {
    const confidence = finding.severity === 'suspect'
      ? el('span', { className: 'confidence', text: `置信 ${percent(finding.confidence)} · Δ ${finding.delta_rows > 0 ? '+' : ''}${finding.delta_rows} 行` })
      : el('span', { className: 'confidence', text: finding.classification || finding.kind });
    const cards = el('div', { className: 'coordinate-grid' }, [coordinateCard('A', finding), coordinateCard('B', finding)]);
    return el('article', { className: 'finding-card', 'data-kind': finding.kind }, [
      el('div', { className: 'finding-head' }, [el('h3', { text: finding.label }), confidence]),
      el('p', { className: 'finding-summary', text: finding.summary }),
      cards,
      evidenceList(finding),
    ]);
  }

  function renderSummary(data) {
    summary.hidden = false;
    const rate = data.a.fps * data.a.rows_per_frame;
    const pills = [
      `整体位移 ${data.global_shift_rows > 0 ? '+' : ''}${data.global_shift_rows} 行`,
      `清单 lead 差 ${data.manifest_lead_difference_rows} 行`,
      `可疑 ${data.suspects.length}`,
      `缺页 ${data.missing_pages.length}`,
      `正常重复声纹 ${data.normal_repeats.length}`,
      `原生率 ${rate} Hz`,
    ];
    summary.replaceChildren(...pills.map((text) => el('span', { className: 'summary-pill', text })));
  }

  function renderFindings(data) {
    list.replaceChildren();
    const head = data.findings.find((item) => item.kind === 'head_offset');
    const groups = [
      ['可疑插行 / 丢行', data.suspects],
      ['卷首偏移', head ? [head] : []],
      ['实物缺页', data.missing_pages],
      ['正常重复声纹（排除）', data.normal_repeats],
    ];
    let rendered = 0;
    for (const [title, items] of groups) {
      if (!items?.length) continue;
      rendered += items.length;
      list.append(el('h3', { className: 'pair-group-title', text: title }));
      list.append(...items.map(findingCard));
    }
    if (!rendered) {
      list.append(el('div', { className: 'empty-pair', text: '没有发现超过阈值的局部行距变化；两份试扫在共同起落上保持一致。' }));
    }
  }

  async function submit(event) {
    event.preventDefault();
    const params = new URLSearchParams(Object.fromEntries(new FormData(form)));
    for (const key of ['profile', 'start', 'end']) if (!params.get(key)) params.delete(key);
    status.dataset.error = '';
    status.textContent = '正在提取双声槽轮廓并对拍…';
    const button = form.querySelector('button');
    button.disabled = true;
    try {
      report = await request(`/captures/line-audit?${params}`);
      renderSummary(report);
      renderFindings(report);
      status.textContent = `对拍完成：${report.a.name} ↔ ${report.b.name}。可复听或点查各结论的原页。`;
    } catch (error) {
      summary.hidden = true;
      list.replaceChildren(el('div', { className: 'empty-pair', text: error.message }));
      status.dataset.error = '';
      status.textContent = error.message;
    } finally {
      button.disabled = false;
    }
  }

  form.addEventListener('submit', submit);
  const query = new URLSearchParams(window.location.search);
  for (const key of ['profile', 'start', 'end']) {
    if (query.get(key)) form.elements[key].value = query.get(key);
  }

  const initialNode = root.querySelector('#pair-initial-report');
  let initialized = false;
  if (initialNode) {
    try {
      report = JSON.parse(initialNode.textContent);
      renderSummary(report);
      renderFindings(report);
      status.textContent = `对拍完成：${report.a.name} ↔ ${report.b.name}。可复听或点查各结论的原页。`;
      initialized = true;
    } catch {
      initialized = false;
    }
  }
  if (!initialized && root.dataset.a && root.dataset.b) {
    form.querySelector('select[name="a"]').value = root.dataset.a;
    form.querySelector('select[name="b"]').value = root.dataset.b;
    submit(new Event('submit', { cancelable: true }));
  }
}

pairPage(document.querySelector('.pair-bench'));
