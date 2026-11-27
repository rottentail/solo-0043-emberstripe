import { captureURL, compactNumber, request } from './client.js';

const statusName = {
  transferable: '可转用',
  insufficient_strips: '共用试条不足',
  reversed_endpoints: '端点倒置',
  endpoint_out_of_range: '端点越界',
  native_unavailable: '本机标定不可用',
};
const laneName = ['左声槽', '右声槽'];

export function transferPanel(root) {
  const form = root.querySelector('#transfer-form');
  const result = root.querySelector('#transfer-result');
  const badge = root.querySelector('#transfer-badge');
  const auditionButton = root.querySelector('#transfer-audition-button');
  const media = root.querySelector('#transfer-audition-media');
  const auditionStatus = root.querySelector('#transfer-audition-status');
  let check, windowData, busy;

  function stripRows(verdict) {
    return verdict.strips.map((strip) => {
      const row = document.createElement('tr');
      const places = strip.patches.map((patch) => `${patch.frame} 格 ${patch.row_start}–${patch.row_end} 行`).join('；');
      row.innerHTML = `<td>${strip.id}</td><td>${places}</td><td>${compactNumber(strip.reference_gray)}</td><td>${compactNumber(strip.target_gray)}</td>`;
      return row;
    });
  }

  function laneCard(verdict) {
    const card = document.createElement('article');
    card.className = 'transfer-lane';
    card.dataset.status = verdict.status;
    const heading = document.createElement('h3');
    heading.textContent = `${laneName[verdict.lane]} · ${statusName[verdict.status] ?? verdict.status}`;
    card.append(heading);
    if (verdict.reason) {
      const reason = document.createElement('p');
      reason.className = 'transfer-reason';
      reason.textContent = verdict.reason;
      card.append(reason);
    }
    const table = document.createElement('table');
    table.className = 'transfer-strips-table';
    table.innerHTML = '<thead><tr><th>试条</th><th>位置</th><th>参考读数</th><th>本机读数</th></tr></thead>';
    const body = document.createElement('tbody');
    body.append(...stripRows(verdict));
    table.append(body);
    card.append(table);
    if (verdict.fit) {
      const fit = document.createElement('p');
      fit.className = 'transfer-fit';
      fit.textContent = `拟合 本机 = ${compactNumber(verdict.fit.slope)} × 参考 + ${compactNumber(verdict.fit.intercept)}`;
      card.append(fit);
    }
    if (verdict.migrated_endpoints) {
      const endpoints = document.createElement('p');
      endpoints.className = 'transfer-endpoints';
      endpoints.textContent = `迁移端点 暗 ${compactNumber(verdict.migrated_endpoints.dark)} ／亮 ${compactNumber(verdict.migrated_endpoints.clear)}`;
      card.append(endpoints);
    }
    return card;
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (busy) return;
    busy = true;
    form.querySelector('button').disabled = true;
    badge.textContent = '正在拟合两台设备的响应…';
    result.hidden = true;
    auditionButton.disabled = true;
    try {
      const params = Object.fromEntries(new FormData(form));
      const strips = params.strips.split(',').map((value) => value.trim()).filter(Boolean);
      check = await request(captureURL(root.dataset.reel, 'transfer-check', {
        reference: params.reference,
        reference_profile: params.reference_profile,
        strips: strips.join(','),
      }));
      renderCheck();
    } catch (error) {
      check = undefined;
      badge.textContent = error.message;
    } finally {
      busy = false;
      form.querySelector('button').disabled = false;
    }
  });

  function renderCheck() {
    badge.textContent = `${check.usable_lanes} / 2 个声槽可转用`;
    result.replaceChildren(...check.lanes.map(laneCard));
    result.hidden = false;
    auditionButton.disabled = check.usable_lanes === 0 || !windowData;
    auditionStatus.textContent = check.usable_lanes === 0 ? '没有声槽通过检查，不能试听。' : '';
  }

  auditionButton.addEventListener('click', () => {
    if (!check || !windowData || check.usable_lanes === 0) return;
    const params = new FormData(form);
    const strips = params.get('strips').split(',').map((value) => value.trim()).filter(Boolean);
    const url = captureURL(root.dataset.reel, 'transfer-audition', {
      reference: params.get('reference'),
      reference_profile: params.get('reference_profile'),
      strips: strips.join(','),
      start: windowData.window.start,
      end: windowData.window.end,
    });
    media.hidden = false;
    auditionStatus.textContent = '正在渲染可转用声槽…';
    auditionButton.disabled = true;
    media.src = url;
    media.load();
  });
  media.addEventListener('loadedmetadata', () => {
    auditionButton.disabled = false;
    const muted = check.lanes.filter((lane) => lane.status !== 'transferable').map((lane) => laneName[lane.lane]);
    auditionStatus.textContent = muted.length ? `未通过声槽已静音：${muted.join('、')}` : '两个声槽均通过转用检查。';
  });
  media.addEventListener('error', () => {
    if (media.hidden) return;
    auditionStatus.textContent = '转用试听载入失败，请重试。';
    auditionButton.disabled = false;
  });

  return {
    setWindow(data) {
      windowData = data;
      if (check && check.usable_lanes > 0) auditionButton.disabled = false;
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
    },
  };
}
