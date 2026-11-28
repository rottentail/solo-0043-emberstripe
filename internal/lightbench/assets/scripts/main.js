import { windowController } from './session.js';
import { film } from './film.js';
import { scope } from './scope.js';
import { probe } from './probe.js';
import { audition } from './audition.js';
import { comparison } from './comparison.js';
import { handoff } from './handoff.js';
import { review } from './review.js';
import { SOUND_PICTURE_LENS, SOUND_KIND, PICTURE_KIND, hasKind } from './review-engine.js';

const root = document.querySelector('main[data-reel]');
const rail = film(root, select);
const waveform = scope(root, select);
const inspector = probe(root, select);
const audio = audition(root, waveform.playback);
const compare = comparison(root);
const parcel = handoff(root);

let boundaries = [];
let lens = SOUND_PICTURE_LENS;

function visibleBounds() {
  // The sound lens hides picture-only cuts; sound cuts (including rows that
  // carry both kinds) remain on the waveform.
  return lens === SOUND_PICTURE_LENS
    ? boundaries
    : boundaries.filter((bound) => !hasKind(bound, PICTURE_KIND) || hasKind(bound, SOUND_KIND));
}

function select(frame, row) {
  rail.select(frame, row);
  waveform.select(frame, row);
  audio.select(frame, row);
  inspector.select(frame, row);
  reviewer.select(frame, row);
}

// mark is a held-jump skim: every panel tracks the cursor, but only the
// release settle asks the delayed probe for this point's evidence.
function mark(frame, row) {
  rail.mark(frame, row);
  waveform.mark(frame, row);
  audio.mark(frame, row);
  inspector.mark(frame, row);
  reviewer.select(frame, row);
}

const reviewer = review(root, {
  onMark: mark,
  onSettle: select,
  onLens(next) {
    lens = next;
    waveform.setReview(visibleBounds());
  },
});

const session = windowController(root, (data) => {
  for (const panel of [rail, waveform, inspector, audio, compare, parcel]) panel.setWindow(data);
  select(data.window.start, 0);
  reviewer.setWindow(data).then((result) => {
    boundaries = result.boundaries;
    lens = reviewer.getLens();
    waveform.setReview(visibleBounds());
  }).catch((error) => {
    boundaries = [];
    waveform.setReview([]);
    root.querySelector('#review-hint').textContent = `复查边界读取失败：${error.message}`;
  });
});
session.start();
