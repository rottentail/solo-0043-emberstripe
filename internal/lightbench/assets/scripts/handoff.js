import { benchURL, compactNumber, request } from './client.js';

// handoff freezes the window currently in force. After a freeze the button
// stays bound to the recorded parcel even if the operator applies another
// window, so the lead can still download exactly what was frozen.
export function handoff(root, onStep) {
  const release = root.querySelector('#release-button');
  const verify = root.querySelector('#verify-button');
  const download = root.querySelector('#parcel-download');
  const scope = root.querySelector('#handoff-scope');
  const status = root.querySelector('#parcel-status');
  let current, receipt, busy = false;
  const address = (id) => `/parcels/${encodeURIComponent(id)}`;
  const stamp = (step) => `${step.window.start}–${step.window.end - 1} 格 · 标定 ${step.profile}`;

  release.addEventListener('click', async () => {
    if (!current || busy) return;
    busy = release.disabled = true;
    verify.hidden = download.hidden = true;
    status.textContent = '正在冻结声轨与画格索引…';
    try {
      const frozen = await request(benchURL(current.reel, 'freeze', {}), { method: 'POST' });
      receipt = frozen.receipt;
      status.textContent = `已冻结 ${stamp(frozen.step)} · ${receipt.id}`;
      download.href = address(receipt.id);
      download.download = `${receipt.id}.zip`;
      verify.hidden = download.hidden = false;
      onStep?.(frozen.step, frozen.trail);
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
      const recorded = data.step?.parcel;
      if (recorded) {
        receipt = undefined;
        download.href = address(recorded);
        download.download = `${recorded}.zip`;
        verify.hidden = download.hidden = false;
        status.textContent = `此步骤已冻结为 ${recorded}。`;
      } else {
        receipt = undefined;
        verify.hidden = download.hidden = true;
        status.textContent = '';
      }
      scope.textContent = data.live
        ? `将冻结 ${data.window.start}–${data.window.end - 1} 格 · 标定 ${data.profile.profile}`
        : `该步骤只能按旧记录查，不能重新冻结。`;
      release.disabled = busy || !data.live;
    },
  };
}
