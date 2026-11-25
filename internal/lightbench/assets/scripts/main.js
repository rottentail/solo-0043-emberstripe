import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';
import { auditCard } from './audit-card.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const inspector = probe(root, select);
const audio = audition(root, waveform.playback);
const compare = comparison(root);
const parcel = handoff(root);
const card = auditCard(root);

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
  card.select(frame, row);
}

const session = windowController(root, (data) => {
  for (const panel of [rail, waveform, inspector, audio, compare, parcel, card]) panel.setWindow(data);
  select(data.window.start, 0);
});

// Reopen a card deep link: prefill the form with the saved window and cursor.
(async function prefill() {
  const params = new URLSearchParams(window.location.search);
  if (!params.has('start') || !params.has('end')) {
    session.start();
    return;
  }
  const form = root.querySelector('#window-form');
  form.elements.start.value = params.get('start');
  form.elements.end.value = params.get('end');
  if (params.has('profile')) form.elements.profile.value = params.get('profile');
  await session.start();
  const frame = Number(params.get('frame') ?? params.get('start'));
  const row = Number(params.get('row') ?? 0);
  if (Number.isInteger(frame) && Number.isInteger(row)) select(frame, row);
})();

