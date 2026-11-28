import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const inspector = probe(root, select);
const audio = audition(root, waveform.playback);
waveform.onRange((start, end) => audio.setRange(start, end));
const compare = comparison(root);
const parcel = handoff(root);

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
}

const session = windowController(root, (data) => {
  for (const panel of [rail, waveform, inspector, audio, compare, parcel]) panel.setWindow(data);
  select(data.window.start, 0);
});
session.start();
