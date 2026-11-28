import { benchURL, request } from './client.js';

const originName = Object.freeze({ reapplicable: '可重新应用', record_only: '仅按旧记录查' });

export function historyPanel(root, onStep) {
  const list = root.querySelector('#trail-list');
  const routes = root.querySelector('#trail-routes');
  const note = root.querySelector('#trail-note');
  const undoButton = root.querySelector('#undo-button');
  const redoButton = root.querySelector('#redo-button');
  const reel = root.dataset.reel;
  let data, busy = false;
  if (!list) return { setWindow() {}, show() {} };

  const move = async (action, params) => {
    if (busy) return;
    busy = undoButton.disabled = redoButton.disabled = true;
    delete note.dataset.error;
    note.textContent = action === 'undo' ? '正在撤回到上一步…' : action === 'redo' ? '正在重做下一步…' : '正在回到所选步骤…';
    try {
      onStep(await request(benchURL(reel, action, params), { method: 'POST' }));
      render();
    } catch (error) {
      note.dataset.error = '';
      note.textContent = error.message;
      render();
    } finally {
      busy = false;
    }
  };

  undoButton.addEventListener('click', () => move('undo', {}));
  redoButton.addEventListener('click', () => move('redo', {}));

  const describe = (entry) => `${entry.window.start}–${entry.window.end - 1} 格 · 标定 ${entry.profile} · 探针 ${entry.probe.frame} 格 ${entry.probe.row} 行`;

  function render() {
    if (!data) return;
    const { entries, routes: lines, current, can_undo, can_redo } = data;
    undoButton.disabled = busy || !can_undo;
    redoButton.disabled = busy || !can_redo;
    delete note.dataset.error;
    note.textContent = current ? `当前步骤 ${current}` : '尚未应用画格窗。';
    list.replaceChildren(...entries.map((entry) => {
      const item = document.createElement('li');
      item.className = 'trail-entry';
      item.dataset.origin = entry.origin.status;
      if (entry.id === current) item.setAttribute('aria-current', 'step');
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'trail-select';
      button.dataset.entry = entry.id;
      button.textContent = `${entry.id} · ${describe(entry)}`;
      button.disabled = entry.origin.status !== 'reapplicable';
      button.addEventListener('click', () => move('focus', { entry: entry.id }));
      const detail = document.createElement('p');
      detail.className = 'trail-origin';
      const status = originName[entry.origin.status] ?? entry.origin.status;
      const frozen = entry.parcel_on_disk ? ` · 冻结件 ${entry.parcel}` : entry.parcel ? ' · 冻结件已不可读' : '';
      detail.textContent = `${status}：${entry.origin.reason}${frozen}`;
      item.append(button, detail);
      return item;
    }));
    routes.replaceChildren(...lines.map((line) => {
      const item = document.createElement('li');
      item.textContent = line.join(' → ');
      if (line.includes(current)) item.dataset.current = '';
      return item;
    }));
  }

  return {
    setWindow(windowData) {
      data = windowData.trail;
      render();
    },
    show(trail) {
      data = trail;
      render();
    },
  };
}
