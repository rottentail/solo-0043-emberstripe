import { captureURL, compactNumber, request } from './client.js';

const SOURCE_PREVIEW = 12;
const MAX_ATTEMPTS = 3;

export function handoff(root) {
  const release = root.querySelector('#release-button');
  const verify = root.querySelector('#verify-button');
  const download = root.querySelector('#parcel-download');
  const scope = root.querySelector('#handoff-scope');
  const status = root.querySelector('#parcel-status');
  let current, receipt, busy = false;
  const address = (id) => `/parcels/${encodeURIComponent(id)}`;
  const preview = (digest) => digest.slice(0, SOURCE_PREVIEW);
  const stamp = ({ window, gauge, source_sha256 }) => `${window.start}–${window.end - 1} 格 · ${gauge.profile} · 源 ${preview(source_sha256)}`;

  // The displayed parcel must trace back to the material approved in the loaded window.
  function ensureApproved(index, approved) {
    if (index.source_sha256 !== approved.source
      || index.window.start !== approved.window.start
      || index.window.end !== approved.window.end
      || index.gauge.profile !== approved.profile) {
      throw new Error('卷材料与批准冻结时不一致，可能重启后同名卷的扫描已更新。该包未显示，请重新装载窗口核对。');
    }
  }

  release.addEventListener('click', async () => {
    if (!current || busy) return;
    const context = current;
    const approved = { source: context.source_sha256, window: context.window, profile: context.profile.profile };
    busy = release.disabled = true;
    verify.hidden = download.hidden = true;
    receipt = undefined;
    try {
      let outcome;
      for (let attempt = 0; attempt < MAX_ATTEMPTS && !outcome; attempt++) {
        try {
          status.textContent = '正在确认交接包是否已在盘上…';
          const found = await request(captureURL(context.reel, 'release', context.params));
          ensureApproved(found.index, approved);
          if (found.stored) {
            outcome = { record: found, existed: true };
            break;
          }
          status.textContent = '盘上还没有该交接包，正在冻结声轨与画格索引…';
          const published = await request(captureURL(context.reel, 'release', context.params), { method: 'POST' });
          ensureApproved(published.index, approved);
          if (published.id !== found.id) throw new Error('冻结结果与确认摘要不一致，请重试。');
          outcome = { record: published, existed: false };
        } catch (error) {
          if (!error.transport) {
            if (error.status === 409) throw new Error('盘上已有的交接包未通过完整性校验，请检查交付目录。');
            throw error;
          }
          status.textContent = '连接中断，正在重新确认交接包是否在盘上…';
        }
      }
      if (!outcome) throw new Error('多次连接中断，仍无法确认交接包是否在盘上，请稍后重试。');
      receipt = { ...outcome.record, approved };
      status.textContent = `${outcome.existed ? '交接包已在盘上（上次中断的冻结或另一窗口已写入）' : '已冻结'} ${stamp(receipt.index)} · ${receipt.id}`;
      download.href = address(receipt.id);
      download.download = `${receipt.id}.zip`;
      verify.hidden = download.hidden = false;
    } catch (error) {
      receipt = undefined;
      status.textContent = `冻结失败。${error.message}`;
    } finally {
      busy = release.disabled = false;
    }
  });

  verify.addEventListener('click', async () => {
    if (!receipt || busy) return;
    const frozen = receipt;
    busy = release.disabled = verify.disabled = true;
    status.textContent = '正在核验包摘要、索引和声轨…';
    try {
      const checked = await request(`${address(frozen.id)}/verify`);
      if (checked.id !== frozen.id) throw new Error('交接包标识与冻结结果不一致。');
      ensureApproved(checked.index, frozen.approved);
      status.textContent = `校验通过 · ${stamp(checked.index)} · ${compactNumber(checked.wave.sample_frames)} 个双声道采样帧`;
    } catch (error) {
      status.textContent = `校验失败。${error.message}`;
    } finally {
      busy = release.disabled = verify.disabled = false;
    }
  });

  return {
    setWindow(data) {
      current = data;
      scope.textContent = `将冻结 ${data.window.start}–${data.window.end - 1} 格 · 标定 ${data.profile.profile} · 源 ${preview(data.source_sha256)}`;
      release.disabled = busy;
    },
  };
}
