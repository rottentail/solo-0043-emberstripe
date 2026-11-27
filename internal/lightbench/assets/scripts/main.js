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
const inspector = probe(root, select, relocate);
const audio = audition(root, waveform.playback, relocate);
const compare = comparison(root);
const parcel = handoff(root);
const panels = [rail, waveform, inspector, audio, compare, parcel];

let cursor = null;
let rows = 0;

function select(frame, row) {
  cursor = { frame, row };
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
}

// relocate moves the probe, the readout and, when the audition is loaded, the
// actual playback head to one picture row. Every relocation action in the
// workbench goes through here, so the panels keep a single landing point.
function relocate(frame, row) {
  select(frame, row);
  audio.relocate(frame * rows + row);
}

// snapshot reads the live probe and the real playback landing point. The
// controller calls it once a response is ready, so a point moved by the
// operator meanwhile is what gets carried into the applied window.
function snapshot() {
  const sound = audio.snapshot();
  return {
    selection: cursor ? cursor.frame * rows + cursor.row : null,
    playhead: sound.playhead,
    playing: sound.playing,
    audio: sound.audio,
  };
}

const session = windowController(root, (data, carry) => {
  rows = data.rows_per_frame;
  for (const panel of panels) panel.setWindow(data);
  // An overlapping window keeps the previously selected picture row; a row
  // that no longer exists falls back to the first row of the applied window
  // while the lost point stays visible for relocation.
  const landing = carry.selection ?? { frame: data.window.start, row: 0 };
  select(landing.frame, landing.row);
  inspector.carry(carry, landing);
  audio.land(carry);
}, snapshot);

session.start();
