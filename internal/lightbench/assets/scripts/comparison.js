import { captureURL, compactNumber, request } from './client.js';

const kinds = Object.freeze({ burst: '短促大偏差', moderate: '持续中等偏差', background: '背景偏差' });
const ordered = ['burst', 'moderate', 'background'];
const percent = (share) => `${compactNumber(share * 100)}%`;

export function comparison(root, onPosition) {
  const form = root.querySelector('#compare-form');
  const output = root.querySelector('#compare-result');
  const detail = root.querySelector('#compare-detail');
  const contributionList = root.querySelector('#compare-contributions');
  const segmentList = root.querySelector('#compare-segments');
  const label = root.querySelector('#active-profile');
  const readings = root.querySelector('#calibration-readings');
  const button = form?.querySelector('button');
  let current, pending, shown;

  function clearDetail() {
    shown = undefined;
    if (!detail) return;
    detail.hidden = true;
    contributionList.replaceChildren();
    segmentList.replaceChildren();
  }

  function render(result, context) {
    const difference = result.difference;
    output.replaceChildren(...[
      `${context.profile} / ${context.against} · ${compactNumber(difference.compared_frames)} 个共同可读位置`,
      `变化样本 ${difference.changed_samples.map(compactNumber).join(' / ')}（左／右）`,
      `峰值差 ${difference.peak_delta.join(' / ')} · 均方根差 ${difference.rms_delta.map(compactNumber).join(' / ')}`,
    ].map((line) => {
      const paragraph = document.createElement('p');
      paragraph.textContent = line;
      return paragraph;
    }));
    if (!detail || !difference.segments?.length) {
      clearDetail();
      return;
    }
    shown = { reel: context.reel, window: context.window, profile: context.profile, against: context.against, difference };
    detail.hidden = false;
    contributionList.replaceChildren(...summarize(difference.contributions ?? []));
    // Strongest contributors first, so the segment that most affects the
    // window's RMS sits at the top of the card.
    const byImpact = [...difference.segments].sort((a, b) => b.energy_share - a.energy_share);
    segmentList.replaceChildren(...byImpact.map((segment, index) => segmentItem(segment, index + 1, shown)));
  }

  function summarize(contributions) {
    const byKind = new Map(contributions.map((entry) => [entry.kind, entry]));
    return ordered.filter((kind) => byKind.has(kind)).map((kind) => {
      const entry = byKind.get(kind);
      const item = document.createElement('li');
      item.dataset.kind = kind;
      item.textContent = `${kinds[kind]} · ${entry.segments} 段／${compactNumber(entry.compared_rows)} 行（${percent(entry.row_share)}）· 贡献 ${percent(entry.energy_share)} 均方能量 · 峰值差 ${entry.peak_delta.join(' / ')}`;
      return item;
    });
  }

  // Each item keeps its own comparison result in its closure, so a later
  // calibration switch cannot redirect it to a different comparison's rows.
  function segmentItem(segment, index, owner) {
    const item = document.createElement('li');
    const button = document.createElement('button');
    button.type = 'button';
    button.dataset.kind = segment.kind;
    button.dataset.start = String(segment.picture_row_start);
    const range = `${segment.picture_row_start}–${segment.picture_row_end - 1} 行 · ${segment.frame} 格 ${segment.row} 行起 · ${compactNumber(segment.compared_rows)} 行`;
    const detailText = `能量 ${percent(segment.energy_share)} · 峰值差 ${segment.peak_delta.join(' / ')} @ ${segment.peak_frame} 格 ${segment.peak_row} 行 · 均方根差 ${segment.rms_delta.map(compactNumber).join(' / ')}`;
    button.textContent = `${index}. ${kinds[segment.kind]} · ${range} · ${detailText}`;
    button.setAttribute('aria-label', `${kinds[segment.kind]}，${range}，${detailText}，定位到第 ${segment.frame} 格第 ${segment.row} 行`);
    button.addEventListener('click', () => locate(button, segment, owner));
    item.append(button);
    return item;
  }

  function locate(button, segment, owner) {
    if (!onPosition || !owner || owner !== shown) return;
    // Only a card rendered from the currently applied window may move the probe.
    if (owner.reel !== current?.reel || owner.window.start !== current?.window.start || owner.window.end !== current?.window.end) return;
    onPosition(segment.frame, segment.row);
    for (const entry of segmentList.querySelectorAll('button')) entry.setAttribute('aria-pressed', String(entry === button));
  }

  form?.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!current || button.disabled) return;
    const active = current;
    const controller = new AbortController();
    pending = controller;
    button.disabled = true;
    output.textContent = '正在对照两个标定…';
    clearDetail();
    try {
      const against = form.elements.against.value;
      const result = await request(captureURL(active.reel, 'compare', { ...active.params, against }), { signal: controller.signal });
      if (active !== current) return;
      render(result, { reel: active.reel, window: active.window, profile: result.left.profile, against: result.right.profile });
    } catch (error) {
      if (!controller.signal.aborted) output.textContent = error.message;
    } finally {
      if (pending === controller) button.disabled = false;
    }
  });

  return {
    setWindow(data) {
      current = data;
      pending?.abort();
      pending = undefined;
      label.textContent = data.profile.profile;
      for (const [index, lane] of ['left', 'right'].entries()) {
        const { dark, clear } = data.profile.lanes[index];
        readings.querySelector(`[data-value="${lane}"]`).textContent = `${compactNumber(dark)} / ${compactNumber(clear)}`;
      }
      clearDetail();
      if (!form) return;
      const select = form.elements.against;
      for (const option of select.options) option.disabled = option.value === data.profile.profile;
      if (select.options[select.selectedIndex]?.disabled) select.value = [...select.options].find((option) => !option.disabled).value;
      output.textContent = '';
      button.disabled = false;
    },
  };
}
