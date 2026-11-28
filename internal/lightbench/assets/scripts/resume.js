import { benchURL, captureURL, compactNumber, request } from './client.js';

const storageKey = (reel) => `emberstripe.pairing.v1.${reel}`;
const probeReasons = {
  'dark reference sheet missing': '暗场参考页缺失',
  'clear reference sheet missing': '亮场参考页缺失',
  'insufficient lane contrast': '声槽对比度不足',
};

function readRecord(reel) {
  try {
    const raw = localStorage.getItem(storageKey(reel));
    if (!raw) return undefined;
    const record = JSON.parse(raw);
    return record?.schema === 1 && record?.reel === reel ? record : undefined;
  } catch {
    return undefined;
  }
}

function writeRecord(reel, record) {
  try {
    localStorage.setItem(storageKey(reel), JSON.stringify(record));
  } catch {
    // Storage may be unavailable; recovery hints are best-effort and must not
    // block the listening workflow itself.
  }
}

function sameFingerprint(a, b) {
  if (!a || !b) return false;
  return a.dark_frame === b.dark_frame && a.clear_frame === b.clear_frame
    && a.dark_rows[0] === b.dark_rows[0] && a.dark_rows[1] === b.dark_rows[1]
    && a.clear_rows[0] === b.clear_rows[0] && a.clear_rows[1] === b.clear_rows[1]
    && a.dark_digest === b.dark_digest && a.clear_digest === b.clear_digest;
}

// Match a saved calibration against the current catalog. Identity is the
// fingerprint, never the id: a rename keeps it while a same-name reference swap
// changes it.
function resolveSide(saved, profiles) {
  const matches = profiles.filter((profile) => profile.usable && sameFingerprint(profile.fingerprint, saved.fingerprint));
  const sameName = profiles.find((profile) => profile.id === saved.id);
  if (matches.length === 1) {
    const profile = matches[0];
    return {
      kind: profile.id === saved.id ? 'exact' : 'renamed',
      profile,
      note: profile.id === saved.id ? '身份确认' : `已改名（保存时为「${saved.id}」）`,
    };
  }
  if (matches.length > 1) {
    return { kind: 'ambiguous', matches, note: '当前卷有多份同身份标定，需人工指认' };
  }
  if (sameName && !sameName.usable) {
    return { kind: 'unusable', note: `同名标定当前不可用：${probeReasons[sameName.reason] ?? sameName.reason}` };
  }
  if (sameName) {
    return { kind: 'swapped', profile: sameName, note: '同名但参考框或参考页已更换，不能视作原标定' };
  }
  return { kind: 'missing', note: '原标定在当前卷中已不存在' };
}

function candidateTag(profile, saved) {
  if (profile.id === saved.id) return '同名换参考框';
  return '接替候选';
}

function readableBasis(data) {
  const basis = new Map();
  data.sources?.forEach((segments) => {
    for (const segment of segments) {
      if (segment.status === 'readable') basis.set(segment.frame, segment.sha256);
    }
  });
  return [...basis.entries()].map(([frame, digest]) => ({ frame, digest }));
}

function paragraph(text) {
  const node = document.createElement('p');
  node.textContent = text;
  return node;
}

export function recovery(root, onVerdicts) {
  const reel = root.dataset.reel;
  const panel = root.querySelector('#resume-panel');
  const summary = root.querySelector('#resume-summary');
  const detail = root.querySelector('#resume-detail');
  const ranges = root.querySelector('#resume-ranges');
  const actions = root.querySelector('#resume-actions');
  const probeOutput = root.querySelector('#resume-probe');
  let profiles = [];
  let record = readRecord(reel);
  let data;
  let primarySide;
  let againstSide;
  let verdicts = [];

  function usableProfiles(excludeFingerprint) {
    return profiles.filter((profile) => profile.usable
      && !(excludeFingerprint && sameFingerprint(profile.fingerprint, excludeFingerprint)));
  }

  function renderSide(title, side, saved) {
    const line = document.createElement('p');
    const badge = document.createElement('strong');
    badge.textContent = `${title}「${saved.id}」：`;
    const state = document.createElement('span');
    state.textContent = side.note;
    state.dataset.state = side.kind;
    line.append(badge, state);
    if (side.kind === 'exact' || side.kind === 'renamed') {
      line.append(` 当前名「${side.profile.id}」`);
    }
    detail.append(line);
    if (saved.gauge) {
      const endpoints = saved.gauge.lanes.map((lane) => `暗 ${compactNumber(lane.dark)} / 亮 ${compactNumber(lane.clear)}`).join(' ｜ ');
      detail.append(paragraph(`保存时端点（左 ｜ 右）：${endpoints}`));
    }
  }

  function renderCandidates(side, saved) {
    if (side.kind === 'exact' || side.kind === 'renamed') return;
    const pool = side.kind === 'ambiguous' ? side.matches : usableProfiles(saved.fingerprint);
    if (pool.length === 0) {
      detail.append(paragraph('当前没有可用于接替的标定。'));
      return;
    }
    const heading = paragraph(side.kind === 'ambiguous' ? '同身份候选：' : '未验证候选（探测不会改写原配对）：');
    detail.append(heading);
    for (const profile of pool) {
      const row = document.createElement('div');
      row.className = 'resume-candidate';
      const label = document.createElement('span');
      label.textContent = `「${profile.id}」· ${candidateTag(profile, saved)}`;
      const probe = document.createElement('button');
      probe.type = 'button';
      probe.className = 'secondary';
      probe.textContent = '在当前位置核对';
      probe.addEventListener('click', () => probeCandidate(profile.id));
      row.append(label, probe);
      detail.append(row);
    }
  }

  function classifyRanges() {
    verdicts = [];
    ranges.replaceChildren();
    if (!data || !record) return;
    const bothConfirmed = primarySide?.profile && againstSide?.profile
      && ['exact', 'renamed'].includes(primarySide.kind) && ['exact', 'renamed'].includes(againstSide.kind);
    const rows = data.rows_per_frame;
    const savedFirst = record.window.start * rows;
    const savedLast = record.window.end * rows;
    const basis = new Map(record.basis.map((entry) => [entry.frame, entry.digest]));
    const name = (absolute) => `${Math.floor(absolute / rows)} 格 ${absolute % rows} 行`;

    data.coverage.forEach((run, index) => {
      let verdict = 'new';
      let note = '';
      if (run.status !== 'readable') {
        verdict = 'blocked';
        note = '该段无声槽样本，无法重放原比较';
      } else if (!bothConfirmed) {
        verdict = 'blocked';
        note = '原配对未完整确认，此段比较暂不可引用';
      } else {
        const outside = run.picture_row_start < savedFirst || run.picture_row_end > savedLast;
        const changedFrame = data.sources[index]?.find((segment) => segment.status === 'readable'
          && basis.get(segment.frame) !== undefined && basis.get(segment.frame) !== segment.sha256);
        const unseenFrame = data.sources[index]?.some((segment) => segment.status === 'readable'
          && !basis.has(segment.frame));
        if (changedFrame) {
          verdict = 'changed';
          note = `源页 ${changedFrame.frame} 的字节与上次听审时不同，原比较在此失效`;
        } else if (outside || unseenFrame) {
          verdict = 'new';
          note = '超出上次复听范围，属于未验证的新比较区';
        } else {
          verdict = 'intact';
          note = '源页与两份标定身份均未变，可继续引用原比较';
        }
      }
      verdicts[index] = verdict;
      const item = document.createElement('button');
      item.type = 'button';
      item.dataset.verdict = verdict;
      item.textContent = `${name(run.picture_row_start)}–${name(run.picture_row_end - 1)} · ${note}`;
      item.addEventListener('click', () => root.dispatchEvent(new CustomEvent('resume-locate', {
        detail: { frame: Math.floor(run.picture_row_start / rows), row: run.picture_row_start % rows },
      })));
      ranges.append(item);
    });
    root.querySelectorAll('[data-resume-legend]').forEach((node) => { node.hidden = verdicts.length === 0; });
    onVerdicts?.();
  }

  async function probeCandidate(candidateId) {
    if (!data) return;
    const frame = Number(root.querySelector('#probe-form').elements.frame.value);
    const row = Number(root.querySelector('#probe-form').elements.row.value);
    probeOutput.replaceChildren(paragraph(`正在用「${data.profile.profile}」与候选「${candidateId}」读取当前位置…`));
    try {
      const params = { frame, row };
      const [primary, candidate] = await Promise.all([
        request(captureURL(reel, 'trace', { ...params, profile: data.profile.profile })),
        request(captureURL(reel, 'trace', { ...params, profile: candidateId })),
      ]);
      probeOutput.replaceChildren();
      if (primary.status !== 'readable' || candidate.status !== 'readable') {
        probeOutput.append(paragraph(`当前位置为${primary.status === 'readable' ? '候选侧不可读' : '不可读位置'}，无法比较偏差。`));
        return;
      }
      const deltas = primary.sample.map((value, lane) => value - candidate.sample[lane]);
      const opposite = primary.sample.some((value, lane) => value !== 0 && candidate.sample[lane] !== 0
        && Math.sign(value) !== Math.sign(candidate.sample[lane]));
      probeOutput.append(
        paragraph(`主标定 ${primary.sample.join(' / ')} · 候选 ${candidate.sample.join(' / ')}`),
        paragraph(`左／右差值 ${deltas.map(compactNumber).join(' / ')} · ${opposite ? '两侧偏差方向相反' : '偏差方向一致'}`),
        paragraph('这只是当前位置的核对，未验证候选不会替换保存的原配对。'),
      );
    } catch (error) {
      probeOutput.replaceChildren(paragraph(`核对失败：${error.message}`));
    }
  }

  function adopt(profileID) {
    const form = root.querySelector('#window-form');
    const select = form.elements.profile;
    const option = [...select.options].find((entry) => entry.value === profileID && !entry.disabled);
    if (option) select.value = profileID;
  }

  function renderActions() {
    actions.replaceChildren();
    if (primarySide && ['exact', 'renamed'].includes(primarySide.kind)) {
      const keep = document.createElement('button');
      keep.type = 'button';
      keep.textContent = `继续用主标定「${primarySide.profile.id}」`;
      keep.addEventListener('click', () => {
        adopt(primarySide.profile.id);
        root.querySelector('#window-form').requestSubmit();
      });
      actions.append(keep);
    }
    if (againstSide && ['exact', 'renamed'].includes(againstSide.kind)) {
      const restore = document.createElement('button');
      restore.type = 'button';
      restore.className = 'secondary';
      restore.textContent = `对照框选回「${againstSide.profile.id}」`;
      restore.addEventListener('click', () => {
        const select = root.querySelector('#compare-form')?.elements.against;
        if (select) select.value = againstSide.profile.id;
      });
      actions.append(restore);
    }
    const clear = document.createElement('button');
    clear.type = 'button';
    clear.className = 'secondary';
    clear.textContent = '清除保存的配对';
    clear.addEventListener('click', () => {
      localStorage.removeItem(storageKey(reel));
      record = undefined;
      render();
    });
    actions.append(clear);
  }

  function render() {
    if (!record) {
      panel.hidden = true;
      return;
    }
    panel.hidden = false;
    summary.replaceChildren();
    detail.replaceChildren();
    ranges.replaceChildren();
    actions.replaceChildren();
    probeOutput.replaceChildren();
    const rows = record.window.end - record.window.start;
    summary.append(paragraph(`上次成功对照：窗 ${record.window.start}–${record.window.end - 1} 格（${rows} 格）· 主标定「${record.primary.id}」对「${record.against.id}」· 保存于 ${new Date(record.savedAt).toLocaleString('zh-CN')}`));
    primarySide = resolveSide(record.primary, profiles);
    againstSide = resolveSide(record.against, profiles);
    renderSide('主标定', primarySide, record.primary);
    renderSide('对照', againstSide, record.against);
    renderCandidates(primarySide, record.primary);
    renderCandidates(againstSide, record.against);
    renderActions();
    classifyRanges();
  }

  async function bootstrap() {
    try {
      const reply = await request(benchURL(reel, 'profiles'));
      profiles = reply.profiles ?? [];
    } catch {
      profiles = [];
      panel.hidden = true;
      return { autoload: undefined };
    }
    render();
    const validWindow = record && record.window && Number.isInteger(record.window.start)
      && record.window.end > record.window.start;
    if (record && primarySide && ['exact', 'renamed'].includes(primarySide.kind) && validWindow) {
      return { autoload: { start: record.window.start, end: record.window.end, profile: primarySide.profile.id } };
    }
    return { autoload: undefined };
  }

  return {
    bootstrap,
    // The window controller hands over every successfully applied window so the
    // saved basis tracks the current source bytes without replacing the pairing.
    setWindow(windowData) {
      data = windowData;
      if (record) {
        record.reelDigest = windowData.reel_digest;
        record.lastWindowAt = new Date().toISOString();
        writeRecord(reel, record);
        if (profiles.length) classifyRanges();
      }
    },
    // Only an explicit, successful two-profile compare may replace the pairing.
    savePairing(windowData, againstID) {
      const primary = profiles.find((profile) => profile.id === windowData.profile.profile);
      const against = profiles.find((profile) => profile.id === againstID);
      if (!primary || !against) return;
      record = {
        schema: 1,
        reel,
        reelDigest: windowData.reel_digest,
        window: { ...windowData.window },
        primary: { id: primary.id, fingerprint: primary.fingerprint, gauge: primary.gauge },
        against: { id: against.id, fingerprint: against.fingerprint, gauge: against.gauge },
        basis: readableBasis(windowData),
        savedAt: new Date().toISOString(),
      };
      writeRecord(reel, record);
      data = windowData;
      render();
    },
    rangeVerdicts: () => verdicts,
  };
}
