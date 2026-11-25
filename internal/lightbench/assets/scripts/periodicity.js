import { benchURL, compactNumber, request, statusName } from './client.js';
import { context } from './canvas.js';

const verdictName = Object.freeze({
  shared_period_supported: '支持同一成因',
  programme_only_not_supported: '不支持同一成因',
  inconclusive: '无法判断',
});

const verdictClass = Object.freeze({
  shared_period_supported: 'verdict-supported',
  programme_only_not_supported: 'verdict-dismissed',
  inconclusive: 'verdict-inconclusive',
});

const seriesName = Object.freeze({ edge: '带边参照', left_lane: '左声槽', right_lane: '右声槽' });

export function periodicity(root) {
  const form = root.querySelector('#periodicity-form');
  const badge = root.querySelector('#periodicity-badge');
  const bandNote = root.querySelector('#periodicity-band');
  const verdictBox = root.querySelector('#periodicity-verdict');
  const pairsBox = root.querySelector('#periodicity-pairs');
  const blocksBox = root.querySelector('#periodicity-blocks');
  const plot = root.querySelector('#periodicity-plot');
  const status = root.querySelector('#periodicity-status');
  const media = root.querySelector('#periodic-media');
  const edgeButton = root.querySelector('#periodic-edge-button');
  const leftButton = root.querySelector('#periodic-left-button');
  const rightButton = root.querySelector('#periodic-right-button');
  let current, report, pending;

  function bandParams() {
    const params = { ...current.params };
    const x0 = form.elements.x0.value;
    const x1 = form.elements.x1.value;
    if (x0 !== '' && x1 !== '') {
      params.x0 = x0;
      params.x1 = x1;
    }
    return params;
  }

  function renderPairs() {
    pairsBox.replaceChildren(...report.pairs.map((pair) => {
      const card = document.createElement('article');
      card.className = `pair-card ${verdictClass[pair.verdict]}`;
      const heading = document.createElement('h3');
      heading.textContent = `${seriesName[pair.lane]} · ${verdictName[pair.verdict]}`;
      const lines = [
        `带边 ${compactNumber(pair.edge_frequency_hz)} Hz（${compactNumber(pair.edge_cycles)} 次重复）`,
        `声槽 ${compactNumber(pair.lane_frequency_hz)} Hz（${compactNumber(pair.lane_cycles)} 次重复）`,
        `频差 ${compactNumber(pair.frequency_gap_hz)} Hz · 分辨力上限 ${compactNumber(pair.resolution_limit_hz)} Hz`,
        `最短可读段 ${compactNumber(pair.shortest_segment_rows)} 行 · 调制深度 ${compactNumber(pair.edge_modulation_depth_gray)} 灰阶`,
        pair.reason,
      ];
      card.append(heading, ...lines.map((text) => {
        const paragraph = document.createElement('p');
        paragraph.textContent = text;
        return paragraph;
      }));
      return card;
    }));
  }

  function renderBlocks() {
    if (!report.unreadable_blocks.length) {
      blocksBox.replaceChildren();
      return;
    }
    const wrapper = document.createElement('div');
    wrapper.className = 'blocks-wrapper';
    const heading = document.createElement('h3');
    heading.textContent = `保留的无法判断位置（${report.unreadable_blocks.length} 段）`;
    const items = report.unreadable_blocks.map((block) => {
      const item = document.createElement('p');
      const source = block.frame < 0 ? '卷界外' : `源 ${block.frame} 格 ${block.row} 行`;
      item.textContent = `${statusName[block.status]} · 画格行 ${block.picture_row_start}–${block.picture_row_end - 1} · ${source}`;
      return item;
    });
    wrapper.append(heading, ...items);
    blocksBox.replaceChildren(wrapper);
  }

  function drawPlot() {
    plot.hidden = false;
    const { paint, width, height } = context(plot);
    paint.clearRect(0, 0, width, height);
    paint.fillStyle = '#1b201e';
    paint.fillRect(0, 0, width, height);
    const colors = { edge: '#f6a069', left_lane: '#a7ccaa', right_lane: '#9db8d8' };
    const rows = report.spectra.length;
    const rowHeight = height / rows;
    const maxFrequency = Math.max(...report.spectra.map((item) => item.peak?.frequency_hz ?? 0));
    const xFor = (frequency) => Math.max(4, frequency / maxFrequency * (width - 8));
    report.spectra.forEach((spectrum, index) => {
      const baseline = (index + 1) * rowHeight - 4;
      paint.strokeStyle = '#4b4d43';
      paint.beginPath();
      paint.moveTo(0, baseline);
      paint.lineTo(width, baseline);
      paint.stroke();
      paint.fillStyle = '#dddacb';
      paint.font = '11px ui-monospace, monospace';
      paint.fillText(seriesName[spectrum.kind], 6, index * rowHeight + 14);
      // The lane's own dominant programme tone stays visible as a faint mark.
      if (spectrum.kind !== 'edge' && spectrum.peak) {
        const dominantX = xFor(spectrum.peak.frequency_hz);
        paint.strokeStyle = '#5d6b60';
        paint.setLineDash([3, 3]);
        paint.beginPath();
        paint.moveTo(dominantX, baseline);
        paint.lineTo(dominantX, index * rowHeight + 18);
        paint.stroke();
        paint.setLineDash([]);
      }
      // The evidence peak is the edge peak for the edge row and the
      // edge-aligned answer for each lane row.
      const evidence = spectrum.kind === 'edge' ? spectrum.peak : spectrum.aligned_peak;
      if (!evidence) return;
      const x = xFor(evidence.frequency_hz);
      paint.strokeStyle = colors[spectrum.kind];
      paint.lineWidth = 2;
      paint.beginPath();
      paint.moveTo(x, baseline);
      paint.lineTo(x, index * rowHeight + 22);
      paint.stroke();
      paint.lineWidth = 1;
      paint.fillStyle = colors[spectrum.kind];
      paint.fillText(`${compactNumber(evidence.frequency_hz)} Hz`, x + 4, index * rowHeight + 26);
    });
  }

  async function verify(event) {
    event?.preventDefault();
    if (!current || form.querySelector('button').disabled) return;
    const controller = new AbortController();
    pending?.abort();
    pending = controller;
    form.querySelector('button').disabled = true;
    badge.textContent = '核验中';
    status.textContent = '正在读取带边参照带与双声槽周期分量…';
    try {
      report = await request(benchURL(current.reel, 'periodicity', bandParams()), { signal: controller.signal });
      if (pending !== controller) return;
      badge.textContent = verdictName[report.overall_verdict];
      verdictBox.className = `periodicity-verdict ${verdictClass[report.overall_verdict]}`;
      verdictBox.textContent = `总体结论：${verdictName[report.overall_verdict]} · 可读 ${compactNumber(report.readable_rows)} 行，缺页 ${compactNumber(report.missing_rows)} 行，卷外 ${compactNumber(report.outside_rows)} 行`;
      bandNote.textContent = `参照带列区 ${report.reference_band.x0}–${report.reference_band.x1 - 1}（左闭右开 ${report.reference_band.x0}..${report.reference_band.x1}）· 采样率 ${compactNumber(report.sample_rate_hz)} Hz`;
      renderPairs();
      renderBlocks();
      drawPlot();
      edgeButton.disabled = leftButton.disabled = rightButton.disabled = false;
      status.textContent = '可复听带边参照，或用左右分轨对照带边与声槽。';
    } catch (error) {
      if (!controller.signal.aborted) {
        badge.textContent = '核验失败';
        status.textContent = error.message;
      }
    } finally {
      if (pending === controller) form.querySelector('button').disabled = false;
    }
  }

  function listen(mode) {
    if (!report) return;
    const params = bandParams();
    params.mode = mode;
    media.hidden = false;
    status.textContent = '正在载入周期证据试听…';
    media.src = benchURL(current.reel, 'periodic-audio', params);
    media.load();
  }

  form.addEventListener('submit', verify);
  edgeButton.addEventListener('click', () => listen('edge'));
  leftButton.addEventListener('click', () => listen('left'));
  rightButton.addEventListener('click', () => listen('right'));
  media.addEventListener('error', () => {
    if (!media.hidden) status.textContent = '试听载入失败，请重试。';
  });
  new ResizeObserver(() => { if (report) drawPlot(); }).observe(plot);

  return {
    setWindow(data) {
      current = data;
      pending?.abort();
      pending = undefined;
      report = undefined;
      form.querySelector('button').disabled = false;
      edgeButton.disabled = leftButton.disabled = rightButton.disabled = true;
      badge.textContent = '';
      bandNote.textContent = `窗口 ${data.window.start}–${data.window.end - 1} 格 · 留空列区使用默认最宽间隙`;
      verdictBox.className = 'periodicity-verdict';
      verdictBox.textContent = '';
      pairsBox.replaceChildren();
      blocksBox.replaceChildren();
      plot.hidden = true;
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      status.textContent = '';
    },
  };
}
