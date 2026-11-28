import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';
import { historyPanel } from './history.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const inspector = probe(root, select);
const audio = audition(root, waveform.playback);
const compare = comparison(root);
const parcel = handoff(root, (step, trail) => trail && trailPanel.show(trail));
const trailPanel = historyPanel(root, (data) => applyStep(data));

function applyStep(data) {
  if (data.applied === false) {
    trailPanel.show(data.trail);
    return;
  }
  session.adopt(data);
}
const session = windowController(root, load, () => inspector.position());

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
}

function load(data) {
  for (const panel of [rail, waveform, inspector, audio, compare, parcel, trailPanel]) panel.setWindow(data);
  // Reopen on the probe recorded with the applied step rather than the window
  // origin, so a reloaded page shows the picture position that was reviewed.
  select(data.step.probe.frame, data.step.probe.row);
}

session.start();
