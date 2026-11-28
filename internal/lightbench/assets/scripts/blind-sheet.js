import { request } from './client.js';

const root = document.querySelector('main.blind-review');
const sheetID = root.dataset.sheet;
const reviewerForm = document.getElementById('reviewer-form');
const reviewerInput = document.getElementById('reviewer-id');
const reviewerStatus = document.getElementById('reviewer-status');
const windowList = document.getElementById('window-list');
const coverageText = Object.freeze({ readable: '可读', missing_sheet: '声槽缺页', outside_capture: '移出卷界' });

let reviewer = localStorage.getItem(`emberstripe:blind:${sheetID}:reviewer`) ?? '';
if (reviewer) reviewerInput.value = reviewer;

async function loadSheet() {
  const query = reviewer ? `?reviewer=${encodeURIComponent(reviewer)}` : '';
  const sheet = await request(`/blind-sheets/${encodeURIComponent(sheetID)}${query}`);
  render(sheet);
  return sheet;
}

function optionURL(index, option) {
  return `/blind-sheets/${encodeURIComponent(sheetID)}/option?window=${index}&option=${option}`;
}

function coverageSummary(coverage) {
  return coverage.map((run) => `${coverageText[run.status] ?? run.status} ${run.picture_row_start}–${run.picture_row_end - 1}`).join('；');
}

function render(sheet) {
  windowList.textContent = '';
  if (sheet.status !== 'open') {
    const notice = document.createElement('p');
    notice.className = 'window-status';
    notice.setAttribute('data-error', '');
    notice.textContent = '答卷已结束作答（已解盲或隔离），以下为只读记录。';
    windowList.appendChild(notice);
  }
  for (const win of sheet.windows) {
    const card = document.createElement('section');
    card.className = 'blind-panel window-card';
    const mine = win.answers.find((answer) => answer.reviewer === reviewer);
    card.innerHTML = `
      <h2>第 ${win.index + 1} 窗 · 画格 ${win.window.start}–${win.window.end - 1}</h2>
      <p class="muted small">共同可听 ${win.common_readable_rows} 行 · 覆盖：${coverageSummary(win.coverage)}</p>
      <div class="option-grid">
        ${['A', 'B'].map((letter) => `
          <div class="option-card" data-option="${letter}">
            <h3>选项 ${letter}</h3>
            <audio controls preload="none" src="${optionURL(win.index, letter)}" aria-label="选项 ${letter} 试听"></audio>
            <label class="pick-line">
              <input type="radio" name="window-${win.index}" value="${letter}" ${mine?.option === letter ? 'checked' : ''} ${sheet.status !== 'open' ? 'disabled' : ''}>
              偏好选项 ${letter}
            </label>
          </div>`).join('')}
      </div>
      <p class="pick-status window-status" role="status"></p>`;
    card.querySelectorAll('input[type=radio]').forEach((radio) => {
      radio.addEventListener('change', () => submitChoice(win.index, radio.value, card));
    });
    windowList.appendChild(card);
  }
}

async function submitChoice(window, option, card) {
  const status = card.querySelector('.pick-status');
  status.removeAttribute('data-error');
  status.textContent = '正在记录偏好…';
  try {
    await request(`/blind-sheets/${encodeURIComponent(sheetID)}/answers`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ window, option, reviewer }),
    });
    status.textContent = `已记录：选项 ${option}`;
  } catch (error) {
    status.setAttribute('data-error', '');
    status.textContent = `记录失败。${error.message}`;
  }
}

reviewerForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const value = reviewerInput.value.trim();
  reviewerStatus.removeAttribute('data-error');
  if (!/^[a-z0-9][a-z0-9-]{1,47}$/.test(value)) {
    reviewerStatus.setAttribute('data-error', '');
    reviewerStatus.textContent = '听审员标识需为 2–48 位小写字母、数字或短横线，且以字母或数字开头。';
    return;
  }
  reviewer = value;
  localStorage.setItem(`emberstripe:blind:${sheetID}:reviewer`, reviewer);
  reviewerStatus.textContent = `正在载入 ${reviewer} 的答卷…`;
  try {
    await loadSheet();
    reviewerStatus.textContent = `听审员 ${reviewer}：选项顺序与每段范围均来自服务端固定答卷，可在其他设备用同一标识继续。`;
  } catch (error) {
    reviewerStatus.setAttribute('data-error', '');
    reviewerStatus.textContent = `载入失败。${error.message}`;
  }
});

if (reviewer) {
  loadSheet().catch((error) => {
    reviewerStatus.setAttribute('data-error', '');
    reviewerStatus.textContent = `载入失败。${error.message}`;
  });
}
