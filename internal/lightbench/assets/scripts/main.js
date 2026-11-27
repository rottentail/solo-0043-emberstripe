import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';
import { review } from './review.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const inspector = probe(root, select);
let activeRows = 0;
const reviewPanel = review(root, (absolute) => {
  select(Math.floor(absolute / activeRows), absolute % activeRows);
});
const audio = audition(root, waveform.playback, reviewPanel.noteSegment);
const compare = comparison(root);
const parcel = handoff(root);

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
}

const session = windowController(root, (data) => {
  activeRows = data.rows_per_frame;
  for (const panel of [rail, waveform, inspector, audio, compare, reviewPanel, parcel]) panel.setWindow(data);
  select(data.window.start, 0);
});
session.start();
