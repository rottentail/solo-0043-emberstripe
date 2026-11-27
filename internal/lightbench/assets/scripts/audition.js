import { captureURL } from './client.js';

export function audition(root, onTime, onRelocate) {
  const button = root.querySelector('#audition-button');
  const media = root.querySelector('#audition-media');
  const message = root.querySelector('#audition-status');
  const relocateButton = root.querySelector('#audition-relocate');
  let data, selected = 0;
  let loading = null;
  let lost = null;

  const origin = () => data.window.start * data.rows_per_frame;
  const length = () => (data.window.end - data.window.start) * data.rows_per_frame;
  const clamp = (absolute) => Math.min(Math.max(0, absolute), length() - 1);

  function dropRelocate() {
    lost = null;
    relocateButton.hidden = true;
  }

  function detach(pending) {
    if (!pending) return;
    media.removeEventListener('loadedmetadata', pending.settle);
    media.removeEventListener('error', pending.fail);
  }

  function unload() {
    detach(loading);
    loading = null;
    media.pause();
    media.hidden = true;
    media.removeAttribute('src');
    media.load();
    button.disabled = false;
    dropRelocate();
  }

  // load starts one audible window at an absolute picture row. Only the latest
  // load may seek or report, so a slow response cannot move the media to an
  // older window after a newer one has been applied.
  function load(absolute, { play = false, note } = {}) {
    if (!data) return;
    detach(loading);
    dropRelocate();
    const pending = { rate: data.rate, offset: clamp(absolute - origin()), play };
    pending.target = origin() + pending.offset;
    pending.settle = () => {
      detach(pending);
      if (loading !== pending) return;
      loading = null;
      media.currentTime = pending.offset / pending.rate;
      button.disabled = false;
      if (!pending.play) {
        message.textContent = '可播放或拖动定位';
        return;
      }
      media.play().catch(() => { message.textContent = '已回到原播放落点，按播放继续。'; });
    };
    pending.fail = () => {
      detach(pending);
      if (loading !== pending) return;
      loading = null;
      message.textContent = '试听载入失败，请重试。';
      button.disabled = false;
    };
    loading = pending;
    button.disabled = true;
    media.hidden = false;
    message.textContent = note ?? '正在载入当前区间…';
    media.addEventListener('loadedmetadata', pending.settle);
    media.addEventListener('error', pending.fail);
    media.src = captureURL(data.reel, 'audition', data.params);
    media.load();
  }

  button.addEventListener('click', () => {
    if (!data) return;
    load(origin() + selected);
  });
  relocateButton.addEventListener('click', () => {
    if (!lost || !data) return;
    // Route through the workbench so the probe, waveform and readout move to
    // the same row that the audition relocates to.
    onRelocate(lost.frame, lost.row);
  });
  media.addEventListener('timeupdate', () => {
    if (media.hidden || !data || loading) return;
    onTime(origin() + Math.round(media.currentTime * data.rate));
  });

  return {
    setWindow(windowData) {
      data = windowData;
      selected = 0;
      unload();
      message.textContent = '';
    },
    select(frame, row) {
      if (!data) return;
      // A manual move supersedes the relocation hint for the previous point.
      dropRelocate();
      selected = clamp((frame - data.window.start) * data.rows_per_frame + row);
      if (!media.hidden && !loading && media.readyState >= 1) media.currentTime = selected / data.rate;
    },
    // land carries the previous actual playback point into the applied window.
    // An overlapping window under the same calibration resumes exactly there;
    // otherwise the status names the reason and offers one relocation action.
    land(carry) {
      dropRelocate();
      if (!carry?.audio) return;
      if (carry.playhead) {
        // The reloaded media lands at the carried playhead, and a later manual
        // reload starts from that same row rather than the probe row.
        selected = clamp(carry.playhead.absolute - origin());
        load(carry.playhead.absolute, {
          play: carry.resume,
          note: carry.resume ? '正在载入并回到原播放落点…' : '正在载入并定位到原播放落点…',
        });
        return;
      }
      if (!carry.lostPlayhead) {
        // Audio was open but had no settled playhead yet; reopen it at the
        // selection so the audition does not silently disappear.
        load(origin() + selected, { play: carry.resume, note: '正在载入新窗口的试听…' });
        return;
      }
      const row = `${carry.lostPlayhead.frame} 格 ${carry.lostPlayhead.row} 行`;
      message.textContent = carry.profileChanged
        ? `标定已更改，原播放落点 ${row} 需按新标定重新定位。`
        : `原播放落点 ${row} 不在新窗口内。`;
      if (carry.nearestPlayhead) {
        lost = { ...carry.nearestPlayhead, play: carry.resume };
        relocateButton.textContent = `定位到最近的 ${lost.frame} 格 ${lost.row} 行`;
        relocateButton.hidden = false;
      }
    },
    // relocate moves a loaded audition to an absolute picture row. It never
    // loads the audio by itself, so an unloaded audition stays unloaded.
    relocate(absolute) {
      if (!data || media.hidden) return;
      load(absolute, { note: '正在载入并定位到该画格行…' });
    },
    snapshot() {
      if (!data || media.hidden) return { audio: false, playhead: null, playing: false };
      // While a load is in flight, report its target rather than a stale clock.
      if (loading) {
        return { audio: true, playhead: loading.target, playing: loading.play };
      }
      const ready = media.readyState >= 1 && !media.seeking && Number.isFinite(media.currentTime);
      return {
        audio: true,
        playhead: ready ? origin() + Math.min(Math.round(media.currentTime * data.rate), length() - 1) : null,
        playing: !media.paused && !media.ended,
      };
    },
  };
}
