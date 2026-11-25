import { request } from './client.js';

const labels = Object.freeze({
  pixel_ready: '可继续查原像素',
  frozen_audio: '仅可回听冻结声音',
  unverified: '无法证明出处',
});

const page = document.querySelector('#audit-card-page');
if (page) {
  const form = page.querySelector('#card-open-form');
  const fileInput = page.querySelector('#card-file');
  const status = page.querySelector('#card-open-status');
  const result = page.querySelector('#card-result');
  const badge = page.querySelector('#result-badge');
  const reason = page.querySelector('#result-reason');
  const facts = page.querySelector('#result-facts');
  const proof = page.querySelector('#result-proof');
  const actions = page.querySelector('#result-actions');
  const playerBox = page.querySelector('#result-player');
  const audio = page.querySelector('#card-audio');

  function fact(term, value) {
    const row = document.createElement('div');
    const dt = document.createElement('dt');
    const dd = document.createElement('dd');
    dt.textContent = term;
    dd.textContent = value;
    row.append(dt, dd);
    return row;
  }

  function flag(text, ok) {
    const item = document.createElement('span');
    item.className = ok ? 'flag ok' : 'flag no';
    item.textContent = `${ok ? '✓' : '✕'} ${text}`;
    return item;
  }

  function render(assessment) {
    result.hidden = false;
    result.dataset.status = assessment.status;
    badge.textContent = labels[assessment.status] ?? assessment.status;
    reason.textContent = assessment.reason;
    const card = assessment.card;
    facts.replaceChildren(
      fact('卷名', card.reel),
      fact('标定', card.profile),
      fact('窗口（半开）', `${card.window.start} – ${card.window.end} 格`),
      fact('正在听的画格行', `第 ${card.cursor.frame} 格 · 第 ${card.cursor.row} 行`),
      fact('源摘要', card.source_sha256),
      fact('冻结包', card.parcel_id),
      fact('音频摘要', card.audio_sha256),
      fact('开卡时间', card.created_at),
    );
    if (card.note) facts.append(fact('交接备注', card.note));

    proof.replaceChildren(
      flag('登记卷与卡内同名', assessment.reel_present),
      flag('源字节摘要一致', assessment.reel_digest_match),
      flag('冻结包通过校验', assessment.parcel_verified),
      flag('冻结包与卡记录一致', assessment.parcel_match),
    );

    actions.replaceChildren();
    playerBox.hidden = true;
    audio.removeAttribute('src');
    audio.load();

    if (assessment.continue_url) {
      const link = document.createElement('a');
      link.className = 'button';
      link.href = assessment.continue_url;
      link.textContent = '回到原卷继续查原像素';
      actions.append(link);
    }
    if (assessment.sound_url) {
      const play = document.createElement('button');
      play.type = 'button';
      play.textContent = '回听当时冻结的声音';
      play.addEventListener('click', () => {
        playerBox.hidden = false;
        audio.src = assessment.sound_url;
        audio.load();
      });
      actions.append(play);
    }
    if (assessment.status === 'unverified') {
      const note = document.createElement('p');
      note.className = 'muted small';
      note.textContent = '请向上一班索取原始扫描卷或完整冻结包后再核验；当前不要把任何新条带当作这张卡的证据。';
      actions.append(note);
    }
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    result.hidden = true;
    status.dataset.error = '';
    const file = fileInput.files?.[0];
    if (!file) {
      status.dataset.error = '';
      status.textContent = '请先选择审听卡 JSON 文件。';
      return;
    }
    status.textContent = '正在核验卡内摘要与手头材料…';
    try {
      const response = await request('/lightbench/audit-card/open', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: await file.text(),
      });
      status.textContent = '';
      render(response);
    } catch (error) {
      status.dataset.error = '';
      status.textContent = error.message;
    }
  });
}
