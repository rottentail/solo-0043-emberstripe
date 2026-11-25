import { captureURL, compactNumber, position, request } from './client.js';

const silenceStatusName = Object.freeze({ silence: '可读低电平', pulse: '超限脉冲', zero_hole: '补零空洞' });

export function silencePanel(root, onPosition) {
  const form = root.querySelector('#silence-form');
  const runButton = form.querySelector('.silence-run');
  const statusLine = root.querySelector('#silence-status');
  const summary = root.querySelector('#silence-summary');
  const lanesLine = root.querySelector('#silence-lanes');
  const pulseVerdict = root.querySelector('#silence-pulse-verdict');
  const framesBox = root.querySelector('#silence-frames');
  const segmentsBox = root.querySelector('#silence-segments');
  const verdictForm = root.querySelector('#verdict-form');
  const refresh = root.querySelector('#verdicts-refresh');
  const verdictsList = root.querySelector('#verdicts-list');
  const clock = root.querySelector('#silence-clock');
  let windowData, analysis;
  let verdictLoad = 0;

  function reviewParams() {
    const formData = Object.fromEntries(new FormData(form));
    return { ...windowData.params, level: formData.level, max_pulse_rows: formData.max_pulse_rows };
  }

  function locate(row) {
    const frame = Math.floor(row / windowData.rows_per_frame);
    if (frame < windowData.window.start || frame >= windowData.window.end) return;
    onPosition(frame, row % windowData.rows_per_frame);
  }

  function segmentButton(segment) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = `silence-segment ${segment.status}`;
    const badges = [];
    if (segment.crosses_frame) badges.push('跨画格');
    if (segment.status === 'pulse' && segment.rows <= analysis.thresholds.max_pulse_rows) badges.push('短脉冲');
    const name = document.createElement('span');
    name.className = 'segment-name';
    name.textContent = silenceStatusName[segment.status];
    for (const badge of badges) {
      const mark = document.createElement('em');
      mark.textContent = badge;
      name.append(' ', mark);
    }
    const meta = document.createElement('span');
    meta.className = 'segment-meta';
    const peak = segment.status === 'zero_hole' ? '' : ` · 峰值 ${segment.peak_abs_sample.join(' / ')}`;
    meta.textContent = `${position(segment.picture_row_start, windowData.rows_per_frame)}起 · ${segment.rows} 行 · 画格 ${segment.frames[0]}–${segment.frames[1]}${peak}`;
    button.append(name, meta);
    button.addEventListener('click', () => locate(segment.picture_row_start));
    return button;
  }

  function render(result) {
    analysis = result;
    summary.hidden = false;
    verdictForm.hidden = false;
    for (const [key, node] of Object.entries(result.totals)) {
      summary.querySelector(`[data-total="${key}"]`).textContent = compactNumber(node);
    }
    lanesLine.textContent = result.lanes.map((lane, index) =>
      `${index === 0 ? '左' : '右'}声道：峰值 ${compactNumber(lane.peak_abs_sample)}，超限 ${compactNumber(lane.over_limit_rows)} 行`).join('　');
    const pulseRows = result.totals.pulse ?? 0;
    const withinPulse = result.pulse_within_limit;
    pulseVerdict.textContent = `脉冲合计 ${compactNumber(pulseRows)} 行，短脉冲上限 ${result.thresholds.max_pulse_rows} 行：${withinPulse ? '在上限内，可按短脉冲核对' : '超出短脉冲上限，需逐段复核'}`;
    pulseVerdict.dataset.flag = withinPulse ? 'ok' : 'warn';
    clock.textContent = `${compactNumber((result.window.end - result.window.start) * result.rows_per_frame)} 个画格行 · 阈值 ${result.thresholds.level}`;

    framesBox.replaceChildren(...result.frames.map((frame) => {
      const row = document.createElement('div');
      row.className = 'silence-frame';
      const id = document.createElement('span');
      id.className = 'frame-id';
      id.textContent = `第 ${String(frame.frame).padStart(2, '0')} 格`;
      const parts = [
        ['silence', frame.silence_rows],
        ['pulse', frame.pulse_rows],
        ['hole', frame.zero_hole_rows],
      ].map(([kind, value]) => {
        const cell = document.createElement('span');
        cell.className = `frame-part ${kind}`;
        cell.textContent = compactNumber(value);
        return cell;
      });
      row.replaceChildren(id, ...parts);
      return row;
    }));
    segmentsBox.replaceChildren(...result.segments.map(segmentButton));
    statusLine.dataset.error = '';
    statusLine.textContent = `审签完成：${result.segments.length} 个区段，补零空洞与可读低电平已分列。`;
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!windowData || runButton.disabled) return;
    runButton.disabled = true;
    delete statusLine.dataset.error;
    statusLine.textContent = '正在按阈值核对左右声槽…';
    try {
      const result = await request(captureURL(windowData.reel, 'silence', reviewParams()));
      render(result);
    } catch (error) {
      statusLine.dataset.error = '';
      statusLine.textContent = error.message;
    } finally {
      runButton.disabled = false;
    }
  });

  verdictForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!analysis) return;
    const fields = Object.fromEntries(new FormData(verdictForm));
    const body = {
      start: analysis.window.start, end: analysis.window.end, profile: analysis.profile,
      level: analysis.thresholds.level, max_pulse_rows: analysis.thresholds.max_pulse_rows,
      signed_by: fields.signed_by, note: fields.note, approved: verdictForm.elements.approved.checked,
    };
    const button = verdictForm.querySelector('button');
    button.disabled = true;
    statusLine.textContent = '正在保存判定…';
    try {
      const saved = await request(captureURL(windowData.reel, 'silence/verdicts', {}), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      statusLine.textContent = `判定 ${saved.id} 已保存，下一班可在下方重开复核。`;
      verdictForm.reset();
      verdictForm.elements.approved.checked = true;
      loadVerdicts();
    } catch (error) {
      statusLine.dataset.error = '';
      statusLine.textContent = `保存失败。${error.message}`;
    } finally {
      button.disabled = false;
    }
  });

  async function loadVerdicts() {
    if (!windowData) return;
    const generation = ++verdictLoad;
    refresh.disabled = true;
    try {
      const verdicts = await request(captureURL(windowData.reel, 'silence/verdicts', {}));
      const cards = await Promise.all(verdicts.map(async (v) => {
        const card = document.createElement('article');
        card.className = 'verdict-card';
        let check = { valid: true, reasons: [] };
        try {
          const reopened = await request(`/captures/${encodeURIComponent(windowData.reel)}/silence/verdicts/${encodeURIComponent(v.id)}`);
          check = reopened.revalidation;
        } catch (error) {
          check = { valid: false, reasons: [error.message] };
        }
        const header = document.createElement('header');
        const title = document.createElement('b');
        title.textContent = v.approved ? '合格判定' : '存疑判定';
        const who = document.createElement('span');
        who.textContent = `${v.signed_by} · ${v.created_at.slice(0, 10)}`;
        header.append(title, who);

        const scopeLine = document.createElement('p');
        scopeLine.textContent = `画格 ${v.window.start}–${v.window.end - 1} · 标定 ${v.profile} · 阈值 ${v.thresholds.level} · 脉冲上限 ${v.thresholds.max_pulse_rows} 行`;
        const totalsLine = document.createElement('p');
        totalsLine.className = 'verdict-totals';
        totalsLine.textContent = `低电平 ${v.analysis.totals.silence} 行 · 脉冲 ${v.analysis.totals.pulse} 行 · 空洞 ${v.analysis.totals.zero_hole} 行`;
        const lineage = document.createElement('p');
        lineage.className = 'verdict-lineage';
        lineage.dataset.valid = String(check.valid);
        lineage.textContent = check.valid ? '当前扫描与标定未变，判定可沿用。' : '判定不可沿用：' + check.reasons.join('；');
        card.append(header, scopeLine, totalsLine, lineage);
        if (v.note) {
          const note = document.createElement('p');
          note.className = 'verdict-note-text';
          note.textContent = v.note;
          card.append(note);
        }
        const jump = document.createElement('button');
        jump.type = 'button';
        jump.className = 'secondary';
        jump.textContent = '定位到判定首行';
        jump.disabled = v.window.start !== windowData.window.start || v.window.end !== windowData.window.end;
        jump.addEventListener('click', () => locate(v.window.start * windowData.rows_per_frame));
        card.append(jump);
        return card;
      }));
      if (generation === verdictLoad) verdictsList.replaceChildren(...cards);
    } finally {
      if (generation === verdictLoad) refresh.disabled = false;
    }
  }
  refresh.addEventListener('click', loadVerdicts);

  return {
    setWindow(data) {
      windowData = data;
      analysis = undefined;
      runButton.disabled = false;
      refresh.disabled = false;
      summary.hidden = verdictForm.hidden = true;
      framesBox.replaceChildren();
      segmentsBox.replaceChildren();
      statusLine.textContent = '应用画格窗后，按阈值审签左右声槽的可读低电平与补零空洞。';
      loadVerdicts();
    },
  };
}
