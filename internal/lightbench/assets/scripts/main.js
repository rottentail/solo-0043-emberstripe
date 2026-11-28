import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { selection } from './selection.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const inspector = probe(root, select);
const choose = selection(root, armRange);
const audio = audition(root, followTime);
const compare = comparison(root);
const parcel = handoff(root);

// select moves the shared probe. The range itself is left untouched so an
// operator can mark a start, keep looking around, then mark the last row.
function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
  choose.select(frame, row);
}

// followTime is called only by playback. It never feeds back into the probe or
// the selection, so a stall, seek or clamp cannot move the chosen end points.
function followTime(absolute) {
  waveform.playback(absolute);
  rail.playback(absolute);
}

function armRange(range) {
  audio.setRange(range);
  rail.setRange(range);
  waveform.setRange(range);
}

const session = windowController(root, (data) => {
  for (const panel of [rail, waveform, inspector, choose, audio, compare, parcel]) panel.setWindow(data);
  armRange(null);
  select(data.window.start, 0);
});
session.start();
