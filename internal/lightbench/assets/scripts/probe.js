import { captureURL, benchURL, compactNumber, request, statusName } from './client.js';
import {
  ABSENT, FAILED, PENDING, PICTURE, MAPPING, READING, STALE, VERIFIED,
  PIECE_LABELS, STATE_LABELS, createProbeLedger,
} from './evidence.js';

// Production transport. Each request carries the ledger key it is serving so
// the ledger itself can reject answers that arrive after a context change.
export function httpTransport() {
  return {
    loadPicture({ reel, frame, signal }) {
      return new Promise((resolve, reject) => {
        const img = new Image();
        img.onload = () => resolve();
        img.onerror = () => reject(new Error('画面扫描页暂时读不回来。'));
        if (signal.aborted) {
          reject(new DOMException('Aborted', 'AbortError'));
          return;
        }
        signal.addEventListener('abort', () => {
          img.onload = img.onerror = null;
          reject(new DOMException('Aborted', 'AbortError'));
        }, { once: true });
        img.src = benchURL(reel, 'strip', { frame });
      });
    },
    fetchMapping({ reel, frame, row, signal }) {
      return request(captureURL(reel, 'register', { frame, row }), { signal });
    },
    fetchReading({ reel, frame, row, profile, signal }) {
      return request(captureURL(reel, 'trace', { frame, row, profile }), { signal });
    },
  };
}

const STATUS_HINT = Object.freeze({
  missing_sheet: '该位置的声槽页本来就没有扫描材料。',
  outside_capture: '该画格行的声槽已移出卷界，卷内没有对应材料。',
});

export function probe(root, onPosition) {
  const form = root.querySelector('#probe-form');
  const badge = root.querySelector('#probe-badge');
  const readings = root.querySelector('#probe-values');
  const ledgerEl = root.querySelector('#evidence-ledger');
  const retryButton = root.querySelector('#evidence-retry');
  const note = root.querySelector('#evidence-note');
  const ledger = createProbeLedger(httpTransport());
  let data;

  function value(key, text = '—') {
    readings.querySelector(`[data-value="${key}"]`).textContent = text;
  }
  function clearReadings() {
    for (const key of ['position', 'source', 'gray', 'sample', 'clipped']) value(key);
  }

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (!data) return;
    onPosition(form.elements.frame.valueAsNumber, form.elements.row.valueAsNumber);
  });

  retryButton.addEventListener('click', () => ledger.retry());

  function renderLedger() {
    const snapshot = ledger.pieces();
    const items = [PICTURE, MAPPING, READING].map((id) => {
      const piece = snapshot[id];
      const li = document.createElement('li');
      li.dataset.piece = id;
      li.dataset.state = piece.state ?? 'idle';
      const label = document.createElement('span');
      label.className = 'evidence-label';
      label.textContent = PIECE_LABELS[id];
      const state = document.createElement('span');
      state.className = 'evidence-state';
      state.textContent = piece.state ? STATE_LABELS[piece.state] : '等待定位';
      li.append(label, state);
      if (piece.state === FAILED || piece.state === STALE) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'secondary evidence-retry-one';
        button.textContent = '重取';
        button.addEventListener('click', () => ledger.retry([id]));
        li.append(button);
      }
      if (piece.error) {
        const detail = document.createElement('small');
        detail.className = 'evidence-error';
        detail.textContent = piece.error;
        li.append(detail);
      }
      if (id === READING && piece.state === ABSENT && piece.data?.status) {
        const detail = document.createElement('small');
        detail.className = 'evidence-material';
        detail.textContent = STATUS_HINT[piece.data.status] ?? '该位置没有可读声槽材料。';
        li.append(detail);
      }
      if (id === PICTURE && piece.state === ABSENT) {
        const detail = document.createElement('small');
        detail.className = 'evidence-material';
        detail.textContent = '该画格本来就没有画面扫描页，声音仍按偏移后的声槽单独核对。';
        li.append(detail);
      }
      return li;
    });
    ledgerEl.replaceChildren(...items);

    const states = Object.values(snapshot).map((piece) => piece.state);
    const anyRecoverable = states.some((state) => state === FAILED || state === STALE);
    retryButton.disabled = !anyRecoverable;
    note.textContent = buildNote(snapshot);
  }

  function buildNote(snapshot) {
    const picture = snapshot[PICTURE];
    const mapping = snapshot[MAPPING];
    const reading = snapshot[READING];
    const parts = [];
    if (picture.state === ABSENT) parts.push('画面材料缺失，不遮蔽偏移后仍可读的声槽');
    if (mapping.state === VERIFIED && mapping.data.status !== 'readable') {
      parts.push(STATUS_HINT[mapping.data.status] ?? '该声槽位置没有可读材料');
    }
    if (reading.state === STALE) parts.push('旧读数属于上一标定，需按当前标定重新取');
    if ([FAILED, STALE].includes(reading.state) && mapping.state === VERIFIED) {
      parts.push('定位与画面证据仍成立，可只重取声槽读数');
    }
    return parts.join('；');
  }

  function renderReadings() {
    clearReadings();
    const context = ledger.context();
    const mapping = ledger.piece(MAPPING);
    const reading = ledger.piece(READING);
    if (!context || context.frame === null) return;

    const absolute = context.frame * context.rowsPerFrame + context.row;
    value('position', `${absolute} 行 · ${compactNumber(absolute * 1000 / context.rate)} 毫秒`);

    if (mapping.state === VERIFIED) {
      value('source', mapping.data.source
        ? `${mapping.data.source.frame} 格 ${mapping.data.source.row} 行`
        : '卷界外');
    } else if (mapping.state === PENDING) {
      value('source', '定位待取…');
    }

    if (reading.state === VERIFIED) {
      const result = reading.data;
      if (result.status === 'readable') {
        value('gray', result.gray.map(compactNumber).join(' / '));
        value('sample', result.sample.join(' / '));
        value('clipped', result.clipped.map((flag) => flag ? '是' : '否').join(' / '));
      } else {
        value('sample', '0 / 0（补零）');
      }
    } else if (reading.state === PENDING) {
      value('sample', '读数待取…');
    } else if (reading.state === ABSENT) {
      value('sample', '0 / 0（补零）');
    }
  }

  function renderBadge() {
    const snapshot = ledger.pieces();
    const states = Object.values(snapshot).map((piece) => piece.state);
    if (states.every((state) => state === null)) {
      badge.textContent = '';
      return;
    }
    if (states.includes(PENDING)) {
      badge.textContent = '按证据取读中';
    } else if (states.includes(FAILED)) {
      badge.textContent = '部分证据待重试';
    } else if (states.includes(STALE)) {
      badge.textContent = '部分证据需重新取';
    } else if (states.every((state) => state === VERIFIED || state === ABSENT)) {
      const reading = snapshot[READING];
      badge.textContent = reading.state === ABSENT
        ? (statusName[reading.data?.status] ?? '材料缺失')
        : (statusName[reading.data?.status] ?? '已核实');
    } else {
      badge.textContent = '';
    }
  }

  ledger.subscribe(() => {
    renderLedger();
    renderReadings();
    renderBadge();
  });

  return {
    setWindow(windowData) {
      data = windowData;
      form.elements.frame.min = data.window.start;
      form.elements.frame.max = data.window.end - 1;
      form.elements.row.max = data.rows_per_frame - 1;
      form.querySelector('button').disabled = false;
      ledger.setWindow(data);
    },
    ledger,
    select(frame, row) {
      form.elements.frame.value = frame;
      form.elements.row.value = row;
      ledger.select(frame, row);
    },
  };
}
