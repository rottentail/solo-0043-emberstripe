import { compactNumber, position } from './client.js';

// Merge half-open integer intervals, so re-listening the same rows never
// expands the heard record.
function mergeRanges(ranges) {
  const merged = [];
  for (const range of [...ranges].sort((a, b) => a.start - b.start)) {
    const last = merged[merged.length - 1];
    if (last && range.start <= last.end) last.end = Math.max(last.end, range.end);
    else merged.push({ ...range });
  }
  return merged;
}

function countRows(ranges) {
  return ranges.reduce((total, range) => total + range.end - range.start, 0);
}

// Restrict heard intervals to rows the current window can actually read.
// Source-slot missing pages and rows outside the capture stay unheard even
// though the WAV plays zeroes over them.
function clipToReadable(ranges, coverage) {
  const readable = coverage.filter((run) => run.status === 'readable');
  const heard = [];
  for (const range of ranges) {
    for (const run of readable) {
      const start = Math.max(range.start, run.picture_row_start);
      const end = Math.min(range.end, run.picture_row_end);
      if (end > start) heard.push({ start, end });
    }
  }
  return mergeRanges(heard);
}

export function review(root, onLocate) {
  const block = root.querySelector('#review-progress');
  const detail = root.querySelector('#review-detail');
  const rangesEl = root.querySelector('#review-ranges');
  const verdict = root.querySelector('#review-verdict');
  let current;
  // One ledger per exact freeze input (window + calibration). Switching the
  // profile or window keeps old records for review, but never lets them count
  // toward a freeze they do not match.
  const ledgers = new Map();

  const keyOf = (data) => `${data.window.start}:${data.window.end}:${data.profile.profile}`;

  function noteSegment(segment) {
    if (!current || segment.end <= segment.start) return;
    const key = keyOf(current);
    const ledger = ledgers.get(key) ?? { start: current.window.start, end: current.window.end, profile: current.profile.profile, ranges: [] };
    ledger.ranges = mergeRanges([...ledger.ranges, segment]);
    ledgers.set(key, ledger);
    render();
  }

  function render() {
    if (!current) return;
    const rows = current.rows_per_frame;
    const readableRuns = current.coverage.filter((run) => run.status === 'readable');
    const readableTotal = readableRuns.reduce((total, run) => total + run.picture_row_end - run.picture_row_start, 0);
    const active = ledgers.get(keyOf(current));
    const heard = active ? clipToReadable(active.ranges, current.coverage) : [];
    const heardRows = countRows(heard);

    detail.textContent = readableTotal
      ? `已听可读声槽 ${compactNumber(heardRows)} / ${compactNumber(readableTotal)} 行（${Math.round(heardRows * 100 / readableTotal)}%）· 标定 ${current.profile.profile}`
      : '当前窗口没有可读声槽行（缺页或卷外），没有可计入听审的范围。';

    const items = heard.map((range) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'secondary review-range';
      button.textContent = `${position(range.start, rows)}起 · ${compactNumber(range.end - range.start)} 行`;
      button.title = `画格行 ${range.start}–${range.end - 1}（半开区间）`;
      button.addEventListener('click', () => onLocate(range.start));
      return button;
    });
    rangesEl.replaceChildren(...items);

    const otherProfiles = [...ledgers.values()]
      .filter((ledger) => ledger.start === current.window.start
        && ledger.end === current.window.end
        && ledger.profile !== current.profile.profile
        && countRows(clipToReadable(ledger.ranges, current.coverage)) > 0)
      .map((ledger) => ledger.profile);

    if (heardRows === 0) {
      verdict.dataset.state = otherProfiles.length ? 'stale' : 'idle';
      verdict.textContent = otherProfiles.length
        ? `当前标定 ${current.profile.profile} 下尚无听审记录；标定 ${otherProfiles.join('、')} 的听审结果不能用于这次冻结，请按当前标定实际播放。`
        : '尚无听审记录。冻结前请先载入试听并实际播放可读范围。';
    } else {
      // Progress was produced under the currently applied calibration, so it is
      // always valid for this freeze; other profiles' records never transfer.
      verdict.dataset.state = 'ready';
      const suffix = otherProfiles.length
        ? ` 切换过的标定（${otherProfiles.join('、')}）另有听审记录，但不适用于本次冻结。`
        : '';
      const coverageText = heardRows === readableTotal ? '可读范围已全部听审，' : '仍有可读范围未听审，';
      verdict.textContent = `${coverageText}此进度随当前标定 ${current.profile.profile} 取得，与本次冻结的画格窗和标定一致，可用于冻结交接。${suffix}`;
    }
  }

  return {
    setWindow(data) {
      current = data;
      block.hidden = false;
      render();
    },
    noteSegment,
  };
}
