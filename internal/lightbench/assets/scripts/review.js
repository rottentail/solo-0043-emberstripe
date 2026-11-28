import { benchURL, position, request, statusName } from './client.js';
import {
  createReviewController,
  createReviewEngine,
  isLens,
  SOUND_LENS,
  SOUND_PICTURE_LENS,
} from './review-engine.js';

const lensName = Object.freeze({ [SOUND_LENS]: '只听声音', [SOUND_PICTURE_LENS]: '声画同查' });

// review wires the keyboard review rail. Held arrow keys skim across stops
// without firing the delayed trace probe; only the release settles on the
// stop currently shown and waits for that point's evidence.
export function review(root, { onMark, onSettle, onLens }) {
  const panel = root.querySelector('#review-panel');
  const lensButtons = [...panel.querySelectorAll('input[name="review-lens"]')];
  const positionOutput = panel.querySelector('#review-position');
  const kindOutput = panel.querySelector('#review-kind');
  const hintOutput = panel.querySelector('#review-hint');
  const backButton = panel.querySelector('#review-back');
  const forwardButton = panel.querySelector('#review-forward');

  let windowData, boundaries = [];
  let engine, controller;

  function describe(row) {
    const bound = boundaries.find((item) => item.row === row);
    if (!bound) return '停点之间，等待跳转';
    const tags = [];
    if (bound.kinds.split(',').includes('sound')) {
      tags.push(`声音 ${statusName[bound.from]} → ${statusName[bound.to]}`);
    }
    if (bound.kinds.split(',').includes('picture')) {
      const depth = (value) => (value ? `${value} 位` : '缺页');
      tags.push(`画面 ${depth(bound.from_depth)} → ${depth(bound.to_depth)}`);
    }
    return tags.join(' · ');
  }

  function render() {
    const row = engine.row;
    positionOutput.textContent = position(row, windowData.rows_per_frame);
    kindOutput.textContent = describe(row);
    const note = engine.atStop() ? '' : '（当前位置不是此口径的停点，可就地细查或继续跳转）';
    hintOutput.textContent = `${lensName[engine.lens]} · ${engine.stops().length} 个停点 ${note}`;
  }

  function mark(row) {
    onMark(Math.floor(row / windowData.rows_per_frame), row % windowData.rows_per_frame);
    render();
  }

  function settle(row) {
    onSettle(Math.floor(row / windowData.rows_per_frame), row % windowData.rows_per_frame);
    render();
  }

  function isTypingTarget(event) {
    const tag = event.target?.tagName;
    return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || event.target?.isContentEditable;
  }

  function switchLens(lens) {
    if (!isLens(lens)) return;
    engine.setLens(lens);
    for (const button of lensButtons) button.checked = button.value === lens;
    onLens(lens);
    render();
  }

  function ensureEngine() {
    if (engine) return;
    const start = windowData.window.start * windowData.rows_per_frame;
    engine = createReviewEngine({ boundaries, lens: SOUND_PICTURE_LENS, row: start });
    controller = createReviewController({ engine, onMark: mark, onSettle: settle });
  }

  window.addEventListener('keydown', (event) => {
    if (!engine || isTypingTarget(event) || event.altKey || event.ctrlKey || event.metaKey) return;
    if (event.key === 'm' || event.key === 'M') {
      event.preventDefault();
      switchLens(engine.toggleLens());
      return;
    }
    const direction = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0;
    if (!direction) return;
    event.preventDefault();
    controller.press(direction, event.repeat);
  });

  window.addEventListener('keyup', (event) => {
    if (!engine) return;
    const direction = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0;
    if (!direction) return;
    event.preventDefault();
    controller.release(direction);
  });

  window.addEventListener('blur', () => controller?.blur());

  for (const button of lensButtons) {
    button.addEventListener('change', () => switchLens(button.value));
  }
  backButton.addEventListener('click', () => {
    ensureEngine();
    engine.move(-1);
    settle(engine.row);
  });
  forwardButton.addEventListener('click', () => {
    ensureEngine();
    engine.move(1);
    settle(engine.row);
  });

  return {
    async setWindow(next) {
      windowData = next;
      const result = await request(benchURL(next.reel, 'review', next.params));
      boundaries = result.boundaries;
      const start = next.window.start * next.rows_per_frame;
      engine = createReviewEngine({
        boundaries,
        lens: engine?.lens ?? SOUND_PICTURE_LENS,
        row: start,
      });
      controller = createReviewController({ engine, onMark: mark, onSettle: settle });
      panel.hidden = false;
      switchLens(engine.lens);
      render();
      settle(engine.row);
      return result;
    },
    select(frame, row) {
      if (!engine) return;
      engine.setRow(frame * windowData.rows_per_frame + row);
      render();
    },
    getLens() {
      return engine?.lens;
    },
  };
}
