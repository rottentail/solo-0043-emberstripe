import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';

const root = document.querySelector('main[data-reel]');
const inspector = probe(root, select);
const ledger = inspector.ledger;
const rail = film(root, select, ledger);
const waveform = scope(root, select);
const audio = audition(root, waveform.playback);
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
  // Keep the current position when it still lies in the new window; switching
  // windows must not discard evidence that remains valid for this picture row.
  const context = ledger.context();
  const keep = context && context.frame !== null
    && data.window.start <= context.frame && context.frame < data.window.end;
  if (keep) {
    select(context.frame, context.row);
  } else {
    select(data.window.start, 0);
  }
});
session.start();
