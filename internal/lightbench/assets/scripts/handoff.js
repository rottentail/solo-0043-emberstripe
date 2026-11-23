import { captureURL, compactNumber, request } from './client.js';

export function handoff(root) {
  const release = root.querySelector('#release-button');
  const verify = root.querySelector('#verify-button');
  const download = root.querySelector('#parcel-download');
  const scope = root.querySelector('#handoff-scope');
  const status = root.querySelector('#parcel-status');
  let current, receipt, busy = false;
  const address = (id) => `/parcels/${encodeURIComponent(id)}`;
  const stamp = ({ window, gauge }) => `${window.start}–${window.end - 1} 格 · ${gauge.profile}`;

  release.addEventListener('click', async () => {
    if (!current || busy) return;
    const context = current;
    busy = release.disabled = true;
    verify.hidden = download.hidden = true;
    status.textContent = '正在冻结声轨与画格索引…';
    try {
      receipt = await request(captureURL(context.reel, 'release', context.params), { method: 'POST' });
      status.textContent = `已冻结 ${stamp(receipt.index)} · ${receipt.id}`;
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
      scope.textContent = `将冻结 ${data.window.start}–${data.window.end - 1} 格 · 标定 ${data.profile.profile}`;
      release.disabled = busy;
    },
  };
}
