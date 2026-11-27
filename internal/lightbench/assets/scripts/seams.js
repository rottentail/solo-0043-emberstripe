import { benchURL, compactNumber, request, statusName } from './client.js';

const verdictName = Object.freeze({
  exposure_shift: '疑似整页曝光变动',
  picture_cut: '像正常画面切换',
  inconclusive: '证据不足',
});

const soundName = Object.freeze({
  shift: '声槽电平跳变',
  steady: '声槽基本平稳',
  missing: '声槽缺页无证据',
});

export function seams(root, onPosition) {
  const reel = root.dataset.reel;
  const list = root.querySelector('#seams-list');
  const summary = root.querySelector('#seams-summary');
  const status = root.querySelector('#seams-status');
  let report;
  let windowData;

  function inWindow(frame) {
    if (!windowData) return false;
    return frame >= windowData.window.start && frame < windowData.window.end;
  }

  function frameRow(address) {
    return { frame: address.frame, row: address.row };
  }

  function jumpButton(target, kind, unusable) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'secondary seam-jump';
    button.disabled = unusable;
    if (unusable) {
      const reason = target.status === 'outside_capture' || target.status === 'missing_sheet'
        ? statusName[target.status] ?? target.status
        : '不在当前窗口';
      button.textContent = `${kind}：${reason}`;
      return button;
    }
    button.textContent = `${kind}：${target.frame} 格 ${target.row} 行`;
    button.addEventListener('click', () => onPosition(target.frame, target.row));
    return button;
  }

  function render() {
    if (!report) return;
    const rows = report.seams.map((seam) => {
      const item = document.createElement('li');
      item.className = `seam-item seam-${seam.decision}`;

      const head = document.createElement('div');
      head.className = 'seam-head';
      const title = document.createElement('span');
      title.className = 'seam-title';
      title.textContent = `第 ${seam.after_frame} → ${seam.after_frame + 1} 页`;
      const badge = document.createElement('span');
      badge.className = 'seam-badge';
      badge.textContent = verdictName[seam.decision] ?? seam.decision;
      head.append(title, badge);

      const metrics = document.createElement('p');
      metrics.className = 'seam-metrics muted';
      metrics.textContent = `整页亮度 ${seam.picture_level_delta >= 0 ? '+' : ''}${compactNumber(seam.picture_level_delta * 100)}% · 结构残差 ${compactNumber(seam.structure_delta)}`;

      const reason = document.createElement('p');
      reason.className = 'seam-reason muted';
      reason.textContent = seam.reason;

      const sound = document.createElement('p');
      sound.className = 'seam-sound muted';
      const deltas = seam.sound_locator.level_delta
        .map((value) => `${value >= 0 ? '+' : ''}${compactNumber(value * 100)}%`)
        .join(' / ');
      sound.textContent = `声音侧：${soundName[seam.sound_locator.evidence] ?? seam.sound_locator.evidence}（左／右电平 ${deltas}）`;

      const jumps = document.createElement('div');
      jumps.className = 'seam-jumps';
      const pictureTarget = frameRow(seam.picture_locator);
      const heardTarget = frameRow(seam.sound_locator.heard_at);
      const pictureUnusable = seam.picture_locator.status !== 'readable' || !inWindow(pictureTarget.frame);
      const heardUnusable = seam.sound_locator.heard_at.status === 'outside_capture'
        || seam.sound_locator.heard_at.status === 'missing_sheet'
        || !inWindow(heardTarget.frame);
      jumps.append(
        jumpButton(pictureTarget, '回到源页', pictureUnusable),
        jumpButton(heardTarget, '实际听到位置', heardUnusable),
      );

      item.append(head, metrics, reason, sound, jumps);
      return item;
    });
    list.replaceChildren(...rows);
    const counts = report.counts;
    summary.textContent = `曝光 ${counts.exposure_shift ?? 0} · 切换 ${counts.picture_cut ?? 0} · 存疑 ${counts.inconclusive ?? 0}`;
  }

  async function load() {
    status.textContent = '';
    try {
      report = await request(benchURL(reel, 'seams', {}));
      render();
    } catch (error) {
      summary.textContent = '接缝读取失败';
      status.textContent = error.message;
    }
  }

  return {
    load,
    setWindow(data) {
      windowData = data;
      if (report) render();
    },
  };
}
