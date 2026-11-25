import { benchURL, request } from './client.js';

// auditCard writes a portable provenance card for the applied window and the
// frame row currently under the cursor. The server supplies every digest, so
// the browser only identifies coordinates and the optional note.
export function auditCard(root) {
  const form = root.querySelector('#audit-card-form');
  const note = root.querySelector('#audit-note');
  const button = root.querySelector('#audit-card-button');
  const status = root.querySelector('#audit-card-status');
  let data, cursor = { frame: 0, row: 0 }, busy = false;

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!data || busy) return;
    busy = true;
    button.disabled = true;
    status.dataset.error = '';
    status.textContent = '正在冻结交接包并写入源摘要…';
    try {
      const response = await request(benchURL(data.reel, 'audit-card'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          start: data.window.start,
          end: data.window.end,
          profile: data.profile.profile,
          frame: cursor.frame,
          row: cursor.row,
          note: note.value.trim(),
        }),
      });
      const blob = new Blob([JSON.stringify(response.card, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `audit-card-${data.reel}-${data.window.start}-${data.window.end - 1}.json`;
      document.body.append(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      status.textContent = `审听卡已生成 · 卷 ${data.reel} · 画格 ${cursor.frame} 第 ${cursor.row} 行 · 包 ${response.parcel_id.slice(0, 12)}…`;
    } catch (error) {
      status.dataset.error = '';
      status.textContent = `审听卡生成失败。${error.message}`;
    } finally {
      busy = false;
      button.disabled = !data;
    }
  });

  return {
    setWindow(windowData) {
      data = windowData;
      cursor = { frame: windowData.window.start, row: 0 };
      note.value = '';
      note.disabled = false;
      button.disabled = busy;
      status.textContent = '';
    },
    select(frame, row) {
      cursor = { frame, row };
    },
  };
}
