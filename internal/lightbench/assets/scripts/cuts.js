import { benchURL, captureURL, compactNumber, position, request } from './client.js';

const warningText = Object.freeze({
  pre_padded_zero: '切前侧含补零',
  post_padded_zero: '切后侧含补零',
  pre_outside_capture: '切前侧移出卷界',
  post_outside_capture: '切后侧移出卷界',
  pre_lane_active: '切前侧声道仍有活动',
  post_lane_inactive: '切后侧无活动起点',
  weak_readable_evidence: '可读证据不足一半',
});

const laneNames = ['左', '右'];

export function cuts(root, onSelect) {
  const status = root.querySelector('#cuts-status');
  const list = root.querySelector('#cut-list');
  const listenBox = root.querySelector('#cut-listen-box');
  const listenLabel = root.querySelector('#cut-listen-label');
  const listenNote = root.querySelector('#cut-listen-note');
  const media = root.querySelector('#cut-listen-media');
  const preButton = root.querySelector('#cut-listen-pre');
  const postButton = root.querySelector('#cut-listen-post');
  const decisionBox = root.querySelector('#cut-decision-box');
  const note = root.querySelector('#cut-note');
  const handoff = root.querySelector('#cut-handoff');
  const exportButton = root.querySelector('#cut-export');
  const download = root.querySelector('#cut-export-download');
  const decisionStatus = root.querySelector('#cut-decision-status');

  let windowData, report, selected, objectURL;

  function sideStats(side) {
    const rows = [];
    rows.push(`画格 ${side.frame_start}–${side.frame_end - 1}（${position(side.row_start, windowData.rows_per_frame)}起）`);
    rows.push(`可读 ${compactNumber(side.readable_rows)} 行 · 缺页 ${compactNumber(side.missing_rows)} · 卷外 ${compactNumber(side.outside_rows)}`);
    rows.push(`补零 ${compactNumber(side.padded_zero_rows)} 行 · 可读静音 ${side.silent_readable.map(compactNumber).join(' / ')}（左／右）`);
    rows.push(`RMS ${side.rms.map((v) => compactNumber(Math.round(v))).join(' / ')} · 峰值 ${side.peak.map(compactNumber).join(' / ')}`);
    return rows;
  }

  function renderCard(candidate) {
    const item = document.createElement('li');
    item.className = 'cut-card';

    const head = document.createElement('button');
    head.type = 'button';
    head.className = 'cut-card-head';
    head.innerHTML = `<span class="cut-frame">第 ${candidate.frame} 格边界</span><span class="cut-score">匹配 ${compactNumber(Math.round(candidate.score * 100))}%</span>`;
    head.setAttribute('aria-pressed', 'false');
    head.addEventListener('click', () => choose(candidate));

    const warnings = document.createElement('div');
    warnings.className = 'cut-warnings';
    if (candidate.warnings.length) {
      for (const code of candidate.warnings) {
        const chip = document.createElement('span');
        chip.className = 'cut-warning';
        chip.textContent = warningText[code] ?? code;
        warnings.append(chip);
      }
    } else {
      const ok = document.createElement('span');
      ok.className = 'cut-ok';
      ok.textContent = '两侧均为可读证据';
      warnings.append(ok);
    }

    const evidence = document.createElement('div');
    evidence.className = 'cut-evidence';
    for (const [label, side] of [['切前', candidate.pre], ['切后', candidate.post]]) {
      const block = document.createElement('div');
      block.className = `cut-side cut-side-${label === '切前' ? 'pre' : 'post'}`;
      const title = document.createElement('p');
      title.className = 'cut-side-title';
      title.textContent = label;
      block.append(title);
      for (const line of sideStats(side)) {
        const row = document.createElement('p');
        row.className = 'cut-side-line';
        row.textContent = line;
        block.append(row);
      }
      evidence.append(block);
    }

    const onset = document.createElement('p');
    onset.className = 'cut-onset';
    onset.textContent = `活动起点 ${candidate.onset_row.map((row, lane) =>
      row < 0 ? `${laneNames[lane]}：未出现` : `${laneNames[lane]}：${position(row, windowData.rows_per_frame)}`).join(' · ')}`;

    item.append(head, warnings, evidence, onset);
    return item;
  }

  function choose(candidate) {
    selected = candidate;
    list.querySelectorAll('.cut-card').forEach((item, index) => {
      item.querySelector('.cut-card-head').setAttribute('aria-pressed', String(report.candidates[index] === candidate));
      item.classList.toggle('selected', report.candidates[index] === candidate);
    });
    onSelect(candidate.frame, 0);
    showListen(candidate);
    showDecision(candidate);
  }

  function spanText(span) {
    return `画格 ${span.start}–${span.end - 1}`;
  }

  function showListen(candidate) {
    listenBox.hidden = false;
    listenLabel.textContent = `切点第 ${candidate.frame} 格 · 听审范围各约 2 格`;
    listenNote.textContent = `切前 ${spanText(candidate.listen_pre)} · 切后 ${spanText(candidate.listen_post)}（半开区间，可分别载入试听）`;
    media.hidden = true;
    media.removeAttribute('src');
    media.load();
  }

  function loadAudition(span) {
    if (!windowData) return;
    const params = { start: span.start, end: span.end, profile: windowData.profile.profile };
    media.hidden = false;
    media.src = captureURL(windowData.reel, 'audition', params);
    media.load();
  }

  preButton.addEventListener('click', () => selected && loadAudition(selected.listen_pre));
  postButton.addEventListener('click', () => selected && loadAudition(selected.listen_post));

  function showDecision(candidate) {
    decisionBox.hidden = false;
    exportButton.disabled = false;
    decisionStatus.textContent = '';
    const rows = [
      ['选定画格', `第 ${candidate.frame} 格边界（画格行 ${compactNumber(candidate.picture_row)}）`],
      ['切前听审', spanText(candidate.listen_pre)],
      ['切后听审', spanText(candidate.listen_post)],
      ['切前证据', `可读 ${candidate.pre.readable_rows} · 补零 ${candidate.pre.padded_zero_rows}`],
      ['切后证据', `可读 ${candidate.post.readable_rows} · 补零 ${candidate.post.padded_zero_rows}`],
    ];
    handoff.replaceChildren(...rows.map(([term, value]) => {
      const row = document.createElement('div');
      row.innerHTML = `<dt></dt><dd></dd>`;
      row.querySelector('dt').textContent = term;
      row.querySelector('dd').textContent = value;
      return row;
    }));
  }

  function buildDecision() {
    return {
      capture: windowData.reel,
      profile: windowData.profile.profile,
      window: windowData.window,
      rate: report.rate,
      rows_per_frame: report.rows_per_frame,
      fps: report.fps,
      selected_cut: { frame: selected.frame, picture_row: selected.picture_row, score: selected.score },
      rationale: note.value.trim(),
      warnings: selected.warnings,
      pre_evidence: selected.pre,
      post_evidence: selected.post,
      listen_pre: selected.listen_pre,
      listen_post: selected.listen_post,
    };
  }

  exportButton.addEventListener('click', () => {
    if (!selected) return;
    if (objectURL) URL.revokeObjectURL(objectURL);
    const blob = new Blob([JSON.stringify(buildDecision(), null, 2)], { type: 'application/json' });
    objectURL = URL.createObjectURL(blob);
    download.href = objectURL;
    download.download = `${windowData.reel}-cut-frame-${selected.frame}.json`;
    download.hidden = false;
    decisionStatus.textContent = `已生成切点决定，听审范围与两侧证据一并写入，可直接交剪辑。`;
  });

  return {
    async setWindow(data) {
      windowData = data;
      selected = undefined;
      list.replaceChildren();
      listenBox.hidden = decisionBox.hidden = true;
      download.hidden = true;
      note.value = '';
      exportButton.disabled = true;
      decisionStatus.textContent = '';
      status.textContent = '正在按声道活动寻找画格切点…';
      try {
        report = await request(benchURL(data.reel, 'cutpoints', data.params));
        if (!report.candidates.length) {
          status.textContent = '该窗口没有可评估的画格边界（至少需要两格）。';
          return;
        }
        list.replaceChildren(...report.candidates.map(renderCard));
        status.textContent = `${report.candidates.length} 个候选 · 阈值 ${report.threshold.map((v) => compactNumber(Math.round(v))).join(' / ')}（左／右 PCM）`;
      } catch (error) {
        status.textContent = `切点分析失败。${error.message}`;
      }
    },
  };
}
