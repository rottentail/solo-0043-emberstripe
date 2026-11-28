import { request } from './client.js';

const reels = JSON.parse(document.getElementById('blind-reels').textContent);
const captureSelect = document.getElementById('blind-capture');
const profileA = document.getElementById('blind-profile-a');
const profileB = document.getElementById('blind-profile-b');
const form = document.getElementById('blind-build-form');
const statusLine = document.getElementById('blind-build-status');

for (const reel of reels) {
  const option = new Option(reel.name, reel.name);
  captureSelect.add(option);
}

function fillProfiles() {
  const reel = reels.find((item) => item.name === captureSelect.value);
  profileA.length = profileB.length = 0;
  for (const id of reel.profiles) {
    profileA.add(new Option(id, id));
    profileB.add(new Option(id, id));
  }
  if (reel.profiles.length > 1) profileB.selectedIndex = 1;
}
captureSelect.addEventListener('change', fillProfiles);
fillProfiles();

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  statusLine.removeAttribute('data-error');
  const data = Object.fromEntries(new FormData(form));
  if (data.profile === data.against) {
    statusLine.setAttribute('data-error', '');
    statusLine.textContent = '请选择两个不同的标定。';
    return;
  }
  const body = {
    capture: data.capture,
    seed: data.seed || undefined,
    windows: [{
      start: Number(data.start),
      end: Number(data.end),
      profile: data.profile,
      against: data.against,
    }],
  };
  statusLine.textContent = '正在校验共同可听区并固定盲选顺序…';
  try {
    const response = await fetch(`/captures/${encodeURIComponent(data.capture)}/blind-sheets`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const payload = await response.json().catch(() => null);
    if (!response.ok) {
      statusLine.setAttribute('data-error', '');
      if (payload?.reason === 'no_common_audible_range') {
        statusLine.textContent = `第 ${payload.window} 窗两份标定在该范围内没有共同可听区，答卷未建立。请调整画格范围。`;
      } else {
        statusLine.textContent = payload?.error ?? '答卷建立失败，请检查输入。';
      }
      return;
    }
    window.location.assign(`/blind/sheets/${payload.id}`);
  } catch (error) {
    statusLine.setAttribute('data-error', '');
    statusLine.textContent = `请求失败。${error.message}`;
  }
});

const output = document.getElementById('unblind-result');
for (const button of document.querySelectorAll('.unblind-button')) {
  button.addEventListener('click', async () => {
    button.disabled = true;
    output.hidden = false;
    output.textContent = '正在解盲并核对当前卷材料…';
    try {
      const result = await request(`/blind-sheets/${encodeURIComponent(button.dataset.sheet)}/unblind`, { method: 'POST' });
      renderUnblind(result);
    } catch (error) {
      output.textContent = `解盲失败。${error.message}`;
      button.disabled = false;
    }
  });
}

const statusText = Object.freeze({
  open: '作答中',
  unblinded: '已解盲',
  quarantined: '已隔离',
});
const reasonText = Object.freeze({
  source_material_changed: '源材料摘要已变化',
  coverage_no_longer_matches: '可读覆盖与建档时不再对应',
  profile_no_longer_valid: '标定已失效',
  no_common_audible_range: '无共同可听区',
});

function renderUnblind(result) {
  output.innerHTML = '';
  const head = document.createElement('div');
  head.className = 'unblind-head';
  head.innerHTML = `<strong>答卷 ${result.id.slice(0, 12)}…</strong> · 卷 ${result.capture} · 状态：${statusText[result.status] ?? result.status}`;
  output.appendChild(head);
  if (result.quarantine_reasons?.length) {
    const note = document.createElement('p');
    note.className = 'quarantine-note';
    note.textContent = `隔离原因：${result.quarantine_reasons.map((reason) => reasonText[reason] ?? reason).join('；')}`;
    output.appendChild(note);
  }
  const list = document.createElement('ol');
  list.className = 'unblind-windows';
  for (const win of result.windows) {
    const item = document.createElement('li');
    const profiles = `A=${win.option_a_profile}，B=${win.option_b_profile}`;
    const picks = win.preferences.length
      ? win.preferences.map((pick) => `${pick.reviewer} 选 ${pick.option} → ${pick.profile}`).join('；')
      : '无答案';
    item.innerHTML = `
      <p>窗 ${win.index} · 画格 ${win.window.start}–${win.window.end - 1} · 共同可听 ${win.common_readable_rows} 行</p>
      <p class="muted">${profiles}</p>
      <p>${picks}</p>`;
    if (win.disagreement) {
      const flag = document.createElement('span');
      flag.className = 'status-pill status-disagree';
      flag.textContent = '听审员有分歧';
      item.appendChild(flag);
    }
    if (win.quarantine_reason) {
      const flag = document.createElement('span');
      flag.className = 'status-pill status-quarantined';
      flag.textContent = reasonText[win.quarantine_reason] ?? win.quarantine_reason;
      item.appendChild(flag);
    }
    const link = document.createElement('a');
    link.className = 'button secondary';
    link.href = win.workbench_url;
    link.textContent = '回到原卷画格';
    item.appendChild(link);
    list.appendChild(item);
  }
  output.appendChild(list);
}
