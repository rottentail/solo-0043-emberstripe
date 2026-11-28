// Pure boundary navigation engine for the two review lenses. It never touches
// the DOM so the same rules can be exercised by the browser and by node tests.
//
// Rows are absolute picture rows. The "sound" lens stops only where the
// optical-sound coverage status changes; the "sound-picture" lens also stops
// at picture sheet changes. Switching lens keeps the confirmed cursor: when a
// picture-only row is not a stop of the new lens the cursor stays on it until
// the next jump leaves from that exact row.

export const SOUND_LENS = 'sound';
export const SOUND_PICTURE_LENS = 'sound-picture';
export const SOUND_KIND = 'sound';
export const PICTURE_KIND = 'picture';

export function isLens(lens) {
  return lens === SOUND_LENS || lens === SOUND_PICTURE_LENS;
}

export function hasKind(boundary, kind) {
  return typeof boundary.kinds === 'string' && boundary.kinds.split(',').includes(kind);
}

// stops returns the sorted stop rows visible through a lens.
export function stops(boundaries, lens) {
  const wanted = lens === SOUND_PICTURE_LENS ? [SOUND_KIND, PICTURE_KIND] : [SOUND_KIND];
  return boundaries
    .filter((boundary) => wanted.some((kind) => hasKind(boundary, kind)))
    .map((boundary) => boundary.row)
    .sort((a, b) => a - b);
}

// boundaryAt finds the boundary descriptor for a row, if one exists.
export function boundaryAt(boundaries, row) {
  return boundaries.find((boundary) => boundary.row === row);
}

// isStop reports whether a row is a stop under the lens.
export function isStop(boundaries, lens, row) {
  const bound = boundaryAt(boundaries, row);
  if (!bound) return false;
  return lens === SOUND_PICTURE_LENS
    ? hasKind(bound, SOUND_KIND) || hasKind(bound, PICTURE_KIND)
    : hasKind(bound, SOUND_KIND);
}

export function createReviewEngine({ boundaries, lens = SOUND_PICTURE_LENS, row } = {}) {
  let currentLens = isLens(lens) ? lens : SOUND_PICTURE_LENS;
  let cursor = row;

  // nextStop walks toward the window edge and lands on the first stop of the
  // active lens strictly beyond the given row. Releasing after a held jump
  // therefore stops exactly where the last jump stopped.
  function nextStop(from, direction) {
    const candidates = stops(boundaries, currentLens);
    if (direction > 0) {
      return candidates.find((stop) => stop > from);
    }
    const behind = candidates.filter((stop) => stop < from);
    return behind.length ? behind[behind.length - 1] : undefined;
  }

  return {
    get lens() {
      return currentLens;
    },
    get row() {
      return cursor;
    },
    setRow(next) {
      if (Number.isInteger(next)) cursor = next;
      return cursor;
    },
    // setLens preserves the confirmed cursor; only the stop set changes.
    setLens(next) {
      if (isLens(next)) currentLens = next;
      return currentLens;
    },
    toggleLens() {
      currentLens = currentLens === SOUND_LENS ? SOUND_PICTURE_LENS : SOUND_LENS;
      return currentLens;
    },
    move(direction) {
      const target = nextStop(cursor, direction);
      if (target !== undefined) cursor = target;
      return cursor;
    },
    // atStop distinguishes a confirmed stop from a picture-only row that the
    // current lens merely passes through during a window/lens switch.
    atStop() {
      return isStop(boundaries, currentLens, cursor);
    },
    boundary() {
      return boundaryAt(boundaries, cursor);
    },
    stops() {
      return stops(boundaries, currentLens);
    },
  };
}

// createReviewController models the held jump key. A press without repeat
// starts a hold; further repeat presses keep skimming to the next stop. Only
// the matching release settles, so a delayed probe is requested exactly once
// for the stop shown when the operator lets go.
export function createReviewController({ engine, onMark = () => {}, onSettle = () => {} }) {
  let heldDirection = 0;

  function jump(direction) {
    const before = engine.row;
    if (engine.move(direction) !== before) onMark(engine.row);
  }

  return {
    press(direction, repeat = false) {
      if (!direction) return false;
      if (repeat) {
        if (heldDirection === direction) jump(direction);
        return true;
      }
      if (heldDirection && heldDirection !== direction) return true;
      heldDirection = direction;
      jump(direction);
      return true;
    },
    release(direction) {
      if (!direction || direction !== heldDirection) return false;
      heldDirection = 0;
      onSettle(engine.row);
      return true;
    },
    // Losing focus while held behaves like a release on the current stop.
    blur() {
      if (!heldDirection) return false;
      heldDirection = 0;
      onSettle(engine.row);
      return true;
    },
    get holding() {
      return heldDirection !== 0;
    },
  };
}
