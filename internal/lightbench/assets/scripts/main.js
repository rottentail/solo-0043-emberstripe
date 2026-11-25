import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { edge } from './edge.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const edges = edge(root, select);
const inspector = probe(root, select);
const audio = audition(root, waveform.playback);
const compare = comparison(root);
const parcel = handoff(root);

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  edges.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
}

const session = windowController(root, (data) => {
  for (const panel of [rail, waveform, edges, inspector, audio, compare, parcel]) panel.setWindow(data);
  select(data.window.start, 0);
});
session.start();
