// Evidence ledger for the frame probe.
//
// The probe assembles three independently sourced pieces of evidence, each tied
// to the context that makes it meaningful:
//
//   - picture: the scanned sheet of the *picture* frame (strip projection).
//   - mapping: where the frame row lands in the offset sound channel.
//   - reading: the calibrated grayscale/PCM reading at that source position.
//
// Window or calibration changes invalidate only the pieces whose identity key
// changes. A picture sheet and an offset sound slot live on different pages, so
// their evidence is tracked separately: a missing picture page never hides a
// still-readable sound slot, and a calibration switch never discards a verified
// mapping. Late responses are accepted only when their key still matches the
// live selection, so an old probe can never paint old sound onto a new picture
// or a new calibration.

export const PICTURE = 'picture';
export const MAPPING = 'mapping';
export const READING = 'reading';

export const VERIFIED = 'verified';
export const PENDING = 'pending';
export const FAILED = 'failed';
export const ABSENT = 'absent';
export const STALE = 'stale';

const PIECES = [PICTURE, MAPPING, READING];

function emptyPiece(id) {
  return { id, key: null, state: null, data: null, error: null };
}

// createProbeLedger owns evidence state. transport supplies the actual HTTP or
// image work and is injected so the whole recovery policy is testable without a
// DOM. Transport methods receive the piece key they are serving.
export function createProbeLedger(transport) {
  const pieces = Object.fromEntries(PIECES.map((id) => [id, emptyPiece(id)]));
  const subscribers = new Set();
  let ctx = null;
  let controllers = new Map();

  function emit(reason) {
    for (const listener of subscribers) {
      try {
        listener(ledger, reason);
      } catch (error) {
        // A broken view must not break evidence bookkeeping for other views.
        console.error(error);
      }
    }
  }

  function keyFor(id) {
    if (!ctx) return null;
    const { reel, frame, row, profile } = ctx;
    if (id === PICTURE) return `${reel}|picture|${frame}`;
    if (id === MAPPING) return `${reel}|mapping|${frame}|${row}`;
    return `${reel}|reading|${frame}|${row}|${profile}`;
  }

  function patch(id, changes) {
    Object.assign(pieces[id], changes);
  }

  function replace(id, key, state, data, error) {
    controllers.delete(id);
    patch(id, { key, state, data, error });
  }

  // Reconcile a piece against the key its current context needs.
  //   - absent material now: record ABSENT without any request;
  //   - key unchanged: keep whatever state and evidence were established;
  //   - key changed: verified evidence becomes STALE (retained, not shown as
  //     current), while failed/absent leftovers are cleared so they refetch.
  function retire(id, nextKey, absentNow) {
    const piece = pieces[id];
    if (absentNow) {
      if (piece.state !== ABSENT || piece.key !== nextKey) replace(id, nextKey, ABSENT, null, null);
      return;
    }
    if (piece.key === nextKey) return;
    if (piece.state === VERIFIED) {
      patch(id, { state: STALE, error: null });
    } else if (piece.state !== null) {
      // Pending/aborted, failed or absent evidence all belonged to the old key;
      // drop it so the new context fetches fresh material.
      patch(id, { state: null, data: null, error: null });
    }
  }

  function startController(id) {
    controllers.get(id)?.abort();
    // Native AbortController works with both fetch and the image loader, and is
    // also available in the Node-based recovery demonstration.
    const controller = new AbortController();
    controllers.set(id, controller);
    return controller;
  }

  function settle(id, key, state, data, error) {
    // Guard against late answers: only the response matching the current key
    // for this piece may update the ledger.
    if (keyFor(id) !== key) return false;
    replace(id, key, state, data, error);
    emit(`settled:${id}`);
    return true;
  }

  function fetchPicture() {
    const { reel, frame } = ctx;
    const key = keyFor(PICTURE);
    const controller = startController(PICTURE);
    const { signal } = controller;
    patch(PICTURE, { key, state: PENDING, data: null, error: null });
    emit('fetch:picture');
    transport.loadPicture({ reel, frame, key, signal })
      .then(() => {
        if (signal.aborted || keyFor(PICTURE) !== key) return;
        settle(PICTURE, key, VERIFIED, { reel, frame }, null);
      })
      .catch((error) => {
        if (signal.aborted || keyFor(PICTURE) !== key) return;
        settle(PICTURE, key, FAILED, null, error.message || '画面读取失败');
      });
  }

  function fetchMapping() {
    const { reel, frame, row } = ctx;
    const key = keyFor(MAPPING);
    const controller = startController(MAPPING);
    const { signal } = controller;
    patch(MAPPING, { key, state: PENDING, data: null, error: null });
    emit('fetch:mapping');
    transport.fetchMapping({ reel, frame, row, key, signal })
      .then((data) => {
        if (signal.aborted || keyFor(MAPPING) !== key) return;
        settle(MAPPING, key, VERIFIED, data, null);
        reconcileReading();
      })
      .catch((error) => {
        if (signal.aborted || keyFor(MAPPING) !== key) return;
        settle(MAPPING, key, FAILED, null, error.message || '声槽定位读取失败');
      });
  }

  // The reading exists only once the mapping proves there is a readable source
  // position. Missing sheet and outside capture are properties of the material,
  // not transport failures, so they are recorded as absent and never retried.
  function reconcileReading() {
    const mapping = pieces[MAPPING];
    const key = keyFor(READING);
    if (mapping.state !== VERIFIED || mapping.key !== keyFor(MAPPING)) return;
    const status = mapping.data.status;
    if (status !== 'readable') {
      if (pieces[READING].state === ABSENT && pieces[READING].key === key) return;
      replace(READING, key, ABSENT, { status }, null);
      emit('settled:reading');
      return;
    }
    const reading = pieces[READING];
    if (reading.key === key && reading.state === VERIFIED) return;
    fetchReading();
  }

  function fetchReading() {
    const { reel, frame, row, profile } = ctx;
    const key = keyFor(READING);
    const controller = startController(READING);
    const { signal } = controller;
    patch(READING, { key, state: PENDING, data: null, error: null });
    emit('fetch:reading');
    transport.fetchReading({ reel, frame, row, profile, key, signal })
      .then((data) => {
        if (signal.aborted || keyFor(READING) !== key) return;
        settle(READING, key, VERIFIED, data, null);
      })
      .catch((error) => {
        if (signal.aborted || keyFor(READING) !== key) return;
        settle(READING, key, FAILED, null, error.message || '声槽读数读取失败');
      });
  }

  // Reconcile all pieces against the current selection after a context change.
  function reconcile() {
    if (!ctx) return;
    const pictureAbsent = ctx.pictureDepth === 0;
    retire(PICTURE, keyFor(PICTURE), pictureAbsent);
    retire(MAPPING, keyFor(MAPPING), false);
    retire(READING, keyFor(READING), false);

    if (pictureAbsent) {
      if (pieces[PICTURE].state !== ABSENT) replace(PICTURE, keyFor(PICTURE), ABSENT, null, null);
    } else if (pieces[PICTURE].state !== PENDING && pieces[PICTURE].state !== VERIFIED) {
      fetchPicture();
    }

    if (pieces[MAPPING].state === PENDING || pieces[MAPPING].state === VERIFIED) {
      reconcileReading();
    } else {
      fetchMapping();
    }
    emit('reconcile');
  }

  const ledger = {
    setWindow(windowData) {
      // A newly applied window or calibration is a new context. In-flight
      // requests are aborted (their answers would fail the key guard anyway),
      // but already settled evidence is retained: when the selection lands on
      // the same picture row, reconcile() reuses the picture and mapping and
      // only refetches the pieces whose identity key changed.
      for (const controller of controllers.values()) controller.abort();
      controllers.clear();
      for (const id of PIECES) {
        if (pieces[id].state === PENDING) patch(id, { state: FAILED, error: '窗口已切换，等待重新取。' });
      }
      ctx = {
        reel: windowData.reel,
        rowsPerFrame: windowData.rows_per_frame,
        rate: windowData.rate,
        window: { ...windowData.window },
        profile: windowData.profile.profile,
        pictureDepthByFrame: new Map(windowData.frames.map((frame) => [frame.frame, frame.depth])),
        frame: null,
        row: null,
      };
      emit('window');
    },

    select(frame, row) {
      if (!ctx) return;
      ctx.frame = frame;
      ctx.row = row;
      ctx.pictureDepth = ctx.pictureDepthByFrame.get(frame) ?? 0;
      reconcile();
    },

    // Retry only the pieces whose evidence could not be established. Verified
    // and absent material is never re-requested unless its context changed
    // again, in which case reconcile() already marked the piece stale or
    // pending. In-flight pieces are left to finish on their own.
    retry(ids = PIECES) {
      if (!ctx || ctx.frame === null) return;
      for (const id of ids) {
        const piece = pieces[id];
        if (![FAILED, STALE].includes(piece.state)) continue;
        if (id === PICTURE) {
          if (ctx.pictureDepth === 0) continue;
          fetchPicture();
        } else if (id === MAPPING) {
          fetchMapping();
        } else if (pieces[MAPPING].state === VERIFIED && pieces[MAPPING].data.status === 'readable') {
          fetchReading();
        }
      }
    },

    piece(id) {
      return { ...pieces[id] };
    },
    pieces() {
      return Object.fromEntries(PIECES.map((id) => [id, this.piece(id)]));
    },
    context() {
      return ctx ? { ...ctx } : null;
    },
    // Cancel every request currently in flight without touching retained
    // evidence; late answers are still rejected by their key guard.
    abort() {
      for (const controller of controllers.values()) controller.abort();
      controllers.clear();
      for (const id of PIECES) {
        if (pieces[id].state === PENDING) patch(id, { state: FAILED, error: '已取消，等待重试。' });
      }
      emit('abort');
    },
    subscribe(listener) {
      subscribers.add(listener);
      return () => subscribers.delete(listener);
    },
  };

  return ledger;
}

export const PIECE_LABELS = Object.freeze({
  [PICTURE]: '画面证据',
  [MAPPING]: '声槽定位',
  [READING]: '声槽读数',
});

export const STATE_LABELS = Object.freeze({
  [VERIFIED]: '已核实',
  [PENDING]: '待取',
  [FAILED]: '待重试',
  [ABSENT]: '材料缺失',
  [STALE]: '需重新取',
});
