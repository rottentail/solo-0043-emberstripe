import { captureURL, request, statusName } from './client.js';

const anchorName = { frame: '画格意见', source: '源行意见' };
const dispositionName = { stays: '留在原画格', moves: '随假设移动' };
const verifiableName = { content: '内容可核实', position_only: '仅位置可核实', anchor_only: '仅原始位置可核实' };

export function annotations(root) {
  const reel = root.dataset.reel;
  const section = root.querySelector('.annotations');
  const badge = section.querySelector('#annotations-badge');
  const file = section.querySelector('#annotation-file');
  const exportLink = section.querySelector('#annotation-export');
  const form = section.querySelector('#hypothesis-form');
  const status = section.querySelector('#annotation-status');
  const list = section.querySelector('#annotation-list');
  const leadField = form.elements.lead;
  leadField.value = section.dataset.lead;

  const place = (p) => `${p.frame} 格 ${p.row} 行`;

  function item(marker, detailText) {
    const entry = document.createElement('li');
    const head = document.createElement('p');
    head.className = 'marker-head';
    head.textContent = `${marker.id} · ${anchorName[marker.anchor]} · 原位置 ${marker.frame} 格 ${marker.row} 行`;
    const detail = document.createElement('p');
    detail.className = 'marker-note';
    detail.textContent = detailText;
    entry.append(head, detail);
    return entry;
  }

  async function load() {
    try {
      const response = await fetch(captureURL(reel, 'annotations'));
      if (response.status === 404) {
        badge.textContent = '尚未导入';
        return;
      }
      if (!response.ok) throw new Error('read');
      const doc = await response.json();
      badge.textContent = `已导入 ${doc.markers.length} 条`;
      exportLink.href = captureURL(reel, 'annotations');
      exportLink.download = `${reel}-annotations.json`;
      exportLink.hidden = false;
      list.replaceChildren(...doc.markers.map((m) => item(m, m.note)));
    } catch {
      badge.textContent = '意见文件读取失败';
    }
  }

  file.addEventListener('change', async () => {
    if (!file.files.length) return;
    status.textContent = '正在导入并回写意见文件…';
    try {
      const text = await file.files[0].text();
      const doc = await request(captureURL(reel, 'annotations'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: text });
      status.textContent = `已回写 ${doc.markers.length} 条意见，两类原始位置均保留。`;
      await load();
    } catch (error) {
      status.textContent = error.message;
    } finally {
      file.value = '';
    }
  });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    status.textContent = '正在按假设偏移核对…';
    try {
      const report = await request(captureURL(reel, 'annotations/projection', { lead: leadField.value }));
      const moved = report.markers.filter((m) => m.disposition === 'moves').length;
      status.textContent = `登记偏移 ${report.registered_lead_rows} 行 · 假设 ${report.hypothesis_lead_rows} 行 · ${moved} 条意见随假设移动`;
      list.replaceChildren(...report.markers.map((m) => item(m,
        `假设位置 ${place(m.picture)} · ${dispositionName[m.disposition]} · ${statusName[m.status]} · ${verifiableName[m.verifiable]} · ${m.note}`)));
    } catch (error) {
      status.textContent = error.message;
    }
  });

  load();
}
