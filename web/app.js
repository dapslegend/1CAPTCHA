'use strict';

const els = {
  stage: document.getElementById('stage'),
  bg: document.getElementById('bg'),
  piece: document.getElementById('piece'),
  refresh: document.getElementById('refresh'),
  status: document.getElementById('status'),
  hint: document.getElementById('hint'),
  panel: document.getElementById('panel'),
  form: document.getElementById('commentForm'),
  comment: document.getElementById('comment'),
  submit: document.getElementById('submitComment'),
  output: document.getElementById('output'),
};

const state = {
  phase: 'loading', // loading | idle | dragging | verifying | passed | failed
  ch: null,
  scale: 1,
  left: 0, // piece bounding-box left, in image pixels
  startPointerX: 0,
  startLeft: 0,
  track: [],
  dragStartedAt: 0,
  pointerType: '',
  ticket: null,
  retryTimer: 0,
};

function setPhase(phase) {
  state.phase = phase;
  els.piece.setAttribute('aria-disabled', String(phase !== 'idle'));
}

function showStatus(text, kind) {
  els.status.textContent = text;
  els.status.className = 'status' + (kind ? ' ' + kind : '');
  els.status.hidden = false;
}

function hideStatus() {
  els.status.hidden = true;
}

function loadImage(img, src) {
  return new Promise((resolve, reject) => {
    img.onload = () => resolve();
    img.onerror = () => reject(new Error('Could not load the challenge image.'));
    img.src = src;
  });
}

async function loadChallenge() {
  clearTimeout(state.retryTimer);
  setPhase('loading');
  els.stage.classList.add('loading');
  els.stage.classList.remove('ok');
  state.ticket = null;
  state.left = 0;
  state.track = [];
  state.dragStartedAt = 0;
  els.panel.hidden = true;
  els.output.hidden = true;
  els.hint.hidden = false;
  els.hint.textContent = 'Hold and drag the piece into the socket it fits.';
  els.refresh.hidden = false;
  els.submit.disabled = false;
  els.submit.textContent = 'Post comment';
  els.piece.classList.remove('done', 'fail', 'dragging');
  hideStatus();

  try {
    const res = await fetch('/api/captcha', { cache: 'no-store' });
    if (!res.ok) {
      throw new Error('Could not load a challenge (HTTP ' + res.status + ').');
    }
    const ch = await res.json();
    state.ch = ch;
    await Promise.all([loadImage(els.bg, ch.bg), loadImage(els.piece, ch.piece)]);
    els.piece.setAttribute('aria-valuemax', String(ch.width - ch.pieceWidth));
    layout();
    setPhase('idle');
    els.stage.classList.remove('loading');
  } catch (err) {
    showStatus(err.message || 'Failed to load the captcha.', 'error');
    setPhase('failed');
    els.stage.classList.remove('loading');
  }
}

function layout() {
  const ch = state.ch;
  if (!ch) return;
  const rendered = els.bg.getBoundingClientRect().width;
  state.scale = rendered > 0 ? rendered / ch.width : 1;
  els.piece.style.width = ch.pieceWidth * state.scale + 'px';
  els.piece.style.height = ch.pieceHeight * state.scale + 'px';
  els.piece.style.top = ch.pieceY * state.scale + 'px';
  renderPiece();
}

function renderPiece() {
  els.piece.style.left = state.left * state.scale + 'px';
  els.piece.setAttribute('aria-valuenow', String(Math.round(state.left)));
}

function clampLeft(value) {
  return Math.min(Math.max(value, 0), Math.max(0, state.ch.width - state.ch.pieceWidth));
}

// Samples x as the piece's left edge so the last sample always matches the
// submitted answer; y is the pointer position. Coalesced event counts, the
// input kind and the browser's isTrusted flag let the server score how human
// the interaction looks.
function addSample(clientY, elapsed, opts) {
  if (!state.ch || state.track.length >= 400) return;
  const options = opts || {};
  const rect = els.bg.getBoundingClientRect();
  const y = (clientY - rect.top) / (state.scale || 1);
  const sample = {
    x: Math.round(state.left * 100) / 100,
    y: Math.round(y * 100) / 100,
    t: Math.round(elapsed),
    k: options.kind || 'p',
  };
  if (options.event) {
    if (options.event.isTrusted) sample.tr = true;
    if (typeof options.event.getCoalescedEvents === 'function') {
      sample.c = Math.max(1, options.event.getCoalescedEvents().length);
    }
  }
  state.track.push(sample);
}

els.piece.addEventListener('pointerdown', (e) => {
  if (state.phase !== 'idle' || e.button !== 0) return;
  e.preventDefault();
  try {
    els.piece.setPointerCapture(e.pointerId);
  } catch {
    // Synthetic pointers (automation, tests) cannot be captured.
  }
  setPhase('dragging');
  els.piece.classList.add('dragging');
  state.pointerType = e.pointerType || '';
  state.startPointerX = e.clientX;
  state.startLeft = state.left;
  state.dragStartedAt = performance.now();
  state.track = [];
  addSample(e.clientY, 0, { event: e });
});

els.piece.addEventListener('pointermove', (e) => {
  if (state.phase !== 'dragging') return;
  const dx = (e.clientX - state.startPointerX) / (state.scale || 1);
  state.left = clampLeft(state.startLeft + dx);
  renderPiece();
  addSample(e.clientY, performance.now() - state.dragStartedAt, { event: e });
});

function finishDrag(e) {
  if (state.phase !== 'dragging') return;
  els.piece.classList.remove('dragging');
  addSample(e.clientY, performance.now() - state.dragStartedAt, { event: e });
  if (state.left < 2) {
    setPhase('idle');
    return;
  }
  verify();
}

els.piece.addEventListener('pointerup', finishDrag);
els.piece.addEventListener('pointercancel', finishDrag);
els.piece.addEventListener('dragstart', (e) => e.preventDefault());

els.piece.addEventListener('keydown', (e) => {
  if (state.phase !== 'idle') return;
  if (!['ArrowLeft', 'ArrowRight', 'Enter', ' '].includes(e.key)) return;
  e.preventDefault();

  if (e.key === 'Enter' || e.key === ' ') {
    if (state.left > 2) verify();
    return;
  }
  if (!state.dragStartedAt) {
    state.dragStartedAt = performance.now();
    state.track = [];
  }
  const step = e.shiftKey ? 1 : 4;
  state.left = clampLeft(state.left + (e.key === 'ArrowRight' ? step : -step));
  renderPiece();
  const rect = els.piece.getBoundingClientRect();
  addSample(rect.top + rect.height / 2, performance.now() - state.dragStartedAt, { kind: 'k' });
});

async function verify() {
  setPhase('verifying');
  showStatus('Checking…', '');

  try {
    const res = await fetch('/api/captcha/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: state.ch.id,
        x: Math.round(state.left),
        track: state.track,
        env: {
          wd: navigator.webdriver === true,
          pt: state.pointerType || undefined,
        },
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok && data.ok) {
      onPassed(data.ticket);
      return;
    }
    onFailed(data.message || 'Verification failed.', data.code);
  } catch {
    onFailed('Network error. Try again.', 'network');
  }
}

function onPassed(ticket) {
  setPhase('passed');
  state.ticket = ticket;
  els.piece.classList.add('done');
  els.stage.classList.add('ok');
  els.refresh.hidden = true;
  els.hint.hidden = true;
  showStatus('Verified', 'ok');
  els.panel.hidden = false;
  els.comment.focus();
}

function onFailed(message, code) {
  setPhase('failed');
  els.piece.classList.add('fail');
  showStatus(message, 'error');
  const delay = code === 'rate_limited' ? 4000 : 1200;
  state.retryTimer = setTimeout(loadChallenge, delay);
}

els.refresh.addEventListener('click', () => {
  if (state.phase === 'loading' || state.phase === 'verifying') return;
  loadChallenge();
});

window.addEventListener('resize', () => {
  if (state.ch) layout();
});

els.form.addEventListener('submit', async (e) => {
  e.preventDefault();
  const text = els.comment.value.trim();
  if (!text || !state.ticket) return;

  els.submit.disabled = true;
  try {
    const res = await fetch('/api/comment', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ticket: state.ticket, text }),
    });
    const data = await res.json().catch(() => ({}));
    els.output.hidden = false;

    if (res.ok && data.ok) {
      state.ticket = null; // tickets are single-use
      els.output.textContent = 'Server accepted the comment.\n\n' + JSON.stringify(data, null, 2);
      els.comment.value = '';
      els.submit.textContent = 'Ticket used — reloading…';
      state.retryTimer = setTimeout(loadChallenge, 1800);
    } else {
      els.output.textContent = 'Rejected: ' + (data.message || 'HTTP ' + res.status);
      els.submit.disabled = false;
    }
  } catch {
    els.output.hidden = false;
    els.output.textContent = 'Network error.';
    els.submit.disabled = false;
  }
});

loadChallenge();
