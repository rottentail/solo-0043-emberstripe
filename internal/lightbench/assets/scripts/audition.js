import { captureURL, compactNumber, position, statusName } from './client.js';

// audition plays the applied window. A continuous check adds a fixed inclusive
// row range: playback starts at that first row, is clamped into the range from
// media events, seeks and animation frames, and halts on the last row. A stall
// or forward seek can never push the interface past the chosen end, and the
// end itself is only ever changed by the selection controls.
export function audition(root, onTime) {
  const button = root.querySelector('#audition-button');
  const checkButton = root.querySelector('#check-button');
  const media = root.querySelector('#audition-media');
  const message = root.querySelector('#audition-status');
  let data, cursor, loadedURL;
  let range;              // {start, end} absolute inclusive rows, end may be absent
  let checking = false;   // a check is armed for the shared media element
  let finished = false;
  let lastRun, loop;

  const origin = () => data.window.start * data.rows_per_frame;
  const lastRow = () => data.window.end * data.rows_per_frame - 1;
  // Media time is quantised to audio samples, so a clamp to the stop time can
  // read back a fraction below it. Half a sample absorbs that without hiding a
  // real seek back inside the range.
  const tolerance = () => 0.5 / data.rate;
  const lowTime = () => (range.start - origin()) / data.rate;
  const stopTime = () => (range.end + 1 - origin()) / data.rate;
  const rowAt = (seconds) => Math.max(origin(), Math.min(lastRow(), origin() + Math.floor(seconds * data.rate + 1e-6)));
  const secondsAt = (row) => (row - origin()) / data.rate;
  const ready = () => range !== undefined && range.end !== undefined;
  const clock = (row) => `${compactNumber(row * 1000 / data.rate)} 毫秒`;

  function seek(seconds) { if (media.readyState >= 1) media.currentTime = seconds; }
  function stopLoop() { if (loop) cancelAnimationFrame(loop); loop = 0; }
  function stopPlayback() { stopLoop(); if (!media.paused) media.pause(); }
  function syncButtons() {
    button.disabled = false;
    checkButton.disabled = !data || !ready();
  }

  function stopAtEnd() {
    if (finished) return;
    finished = true;
    seek(stopTime());
    stopPlayback();
    onTime(range.end);
    message.textContent = `已到末行 ${position(range.end, data.rows_per_frame)} · 停在原时间 ${clock(range.end)}。`;
  }

  function follow(absolute) {
    if (finished) return;
    onTime(absolute);
    const run = data.coverage.find((item) => absolute >= item.picture_row_start && absolute < item.picture_row_end);
    if (run === lastRun) return;
    lastRun = run;
    const status = run?.status ?? 'outside_capture';
    message.textContent = status === 'readable'
      ? `连续检查 · ${position(absolute, data.rows_per_frame)}`
      : `经过原位置 ${position(absolute, data.rows_per_frame)} · ${statusName[status]}（按原时间计时）`;
  }

  // clamp keeps the play head inside the stored range. It never revises the
  // chosen end, even if the media element jumped ahead while the page stalled.
  function clamp() {
    if (!checking || !ready()) return false;
    if (media.currentTime > stopTime() - tolerance()) {
      seek(stopTime());
      stopAtEnd();
      return true;
    }
    if (media.currentTime < lowTime() - tolerance()) {
      seek(lowTime());
      finished = false;
      onTime(range.start);
      return true;
    }
    return false;
  }

  function tick() {
    loop = 0;
    if (!data || media.paused || media.hidden) return;
    if (!clamp()) {
      if (checking && ready()) follow(Math.min(range.end, rowAt(media.currentTime)));
      else onTime(rowAt(media.currentTime));
    }
    if (!media.paused) loop = requestAnimationFrame(tick);
  }

  function ensureLoop() {
    if (!loop && data && !media.hidden) loop = requestAnimationFrame(tick);
  }

  function play() {
    const attempt = media.play();
    if (attempt) attempt.catch(() => { message.textContent = '已定位到选区起点，请按播放键开始连续检查。'; });
  }

  function arm() {
    button.disabled = checkButton.disabled = true;
    media.hidden = false;
    message.textContent = '正在载入当前区间…';
    const url = captureURL(data.reel, 'audition', data.params);
    if (loadedURL === url && media.readyState >= 1) {
      syncButtons();
      return;
    }
    if (loadedURL !== url) { media.src = url; loadedURL = url; }
    media.load();
  }

  function beginCheck() {
    if (!data || !ready()) return;
    stopPlayback();
    checking = true;
    finished = false;
    lastRun = undefined;
    arm();
    if (media.readyState >= 1) {
      seek(lowTime());
      message.textContent = `连续检查从 ${position(range.start, data.rows_per_frame)} 起。`;
      play();
    }
  }

  button.addEventListener('click', () => {
    if (!data) return;
    stopPlayback();
    checking = false;
    finished = false;
    lastRun = undefined;
    arm();
    if (media.readyState >= 1) {
      seek(cursor ? secondsAt(cursor.absolute) : 0);
      message.textContent = '可播放或拖动定位';
    }
  });

  checkButton.addEventListener('click', beginCheck);

  media.addEventListener('loadedmetadata', () => {
    syncButtons();
    if (!data) return;
    if (checking && ready()) {
      seek(lowTime());
      message.textContent = `连续检查从 ${position(range.start, data.rows_per_frame)} 起。`;
      play();
    } else {
      seek(cursor ? secondsAt(cursor.absolute) : 0);
      message.textContent = '可播放或拖动定位';
    }
  });

  media.addEventListener('play', () => {
    if (!data) return;
    if (checking && ready() && (finished || media.currentTime < lowTime() - tolerance())) {
      finished = false;
      seek(lowTime());
    }
    ensureLoop();
  });
  media.addEventListener('pause', stopLoop);
  media.addEventListener('timeupdate', () => {
    if (!data || media.hidden || finished) return;
    if (clamp()) return;
    if (checking && ready()) follow(Math.min(range.end, rowAt(media.currentTime)));
  });
  media.addEventListener('seeking', () => {
    if (!data || !checking || !ready()) return;
    const low = lowTime(), high = stopTime();
    if (media.currentTime > high - tolerance()) { seek(high); stopAtEnd(); return; }
    // Only a real seek back inside the range re-arms progress reporting; the
    // rounding of a clamp to the stop time must keep the final message intact.
    if (media.currentTime < high - tolerance()) finished = false;
    if (media.currentTime < low - tolerance()) { seek(low); onTime(range.start); }
  });
  media.addEventListener('seeked', () => {
    if (!data || media.hidden) return;
    if (clamp()) return;
    if (finished) { onTime(range.end); return; }
    onTime(Math.min(checking && ready() ? range.end : lastRow(), rowAt(media.currentTime)));
  });
  media.addEventListener('ended', () => { if (checking && ready()) stopAtEnd(); });
  media.addEventListener('waiting', () => {
    // A stall while a check is running keeps the cursor and range; the buffering
    // notice must not replace the final "reached the last row" message. Clear
    // the run memo so progress is announced again once data resumes.
    if (media.hidden || !data || finished || media.paused) return;
    lastRun = undefined;
    message.textContent = '音频缓冲中，位置与选区保持不变。';
  });
  media.addEventListener('error', () => {
    if (media.hidden) return;
    stopPlayback();
    syncButtons();
    message.textContent = '试听载入失败，请重试。';
  });

  return {
    setWindow(windowData) {
      data = windowData;
      cursor = undefined;
      range = undefined;
      loadedURL = undefined;
      checking = false;
      finished = false;
      lastRun = undefined;
      stopPlayback();
      media.hidden = true;
      media.removeAttribute('src');
      media.load();
      button.disabled = false;
      checkButton.disabled = true;
      message.textContent = '';
    },
    setRange(next) {
      range = next ?? undefined;
      if (checking && !ready()) {
        stopPlayback();
        checking = false;
      }
      checkButton.disabled = !data || !ready();
      if (checking && ready() && media.readyState >= 1) seek(lowTime());
    },
    select(frame, row) {
      if (!data) return;
      cursor = { frame, row, absolute: frame * data.rows_per_frame + row };
      if (checking && ready()) {
        // A probe inside the fixed range only moves the play head; both end
        // points stay put. Probing outside the range deliberately leaves the
        // check so the cursor and the sound clock cannot disagree.
        if (cursor.absolute >= range.start && cursor.absolute <= range.end) {
          seek(secondsAt(cursor.absolute));
          finished = false;
          onTime(cursor.absolute);
          return;
        }
        stopPlayback();
        checking = false;
        message.textContent = '已离开连续检查选区，点击「连续检查」可从起点重新开始。';
      }
      if (!media.hidden && media.readyState >= 1) seek(secondsAt(cursor.absolute));
    },
  };
}
