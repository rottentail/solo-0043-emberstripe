import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';
import { recovery } from './resume.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
let waveform;
const recover = recovery(root, () => waveform?.refresh());
waveform = scope(root, select, () => recover.rangeVerdicts());
const inspector = probe(root, select);
const audio = audition(root, waveform.playback);
const compare = comparison(root, (windowData, against) => recover.savePairing(windowData, against));
const parcel = handoff(root);

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
}

root.addEventListener('resume-locate', (event) => {
  select(event.detail.frame, event.detail.row);
});

const session = windowController(root, (data) => {
  for (const panel of [rail, waveform, inspector, audio, compare, parcel, recover]) panel.setWindow(data);
  select(data.window.start, 0);
});

recover.bootstrap().then(({ autoload }) => {
  session.start(autoload);
});
