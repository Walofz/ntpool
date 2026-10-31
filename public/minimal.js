// Minimal ntpool dashboard: one websocket, a handful of DOM nodes, no state bloat.

const MAX_EVENTS = 30;

const els = {
  liveDot: document.getElementById('live-dot'),
  poolName: document.getElementById('pool-name'),
  network: document.getElementById('network'),
  pillRpc: document.getElementById('pill-rpc'),
  pillZmq: document.getElementById('pill-zmq'),
  pillPool: document.getElementById('pill-pool'),
  hashrate: document.getElementById('hashrate'),
  hashrateSub: document.getElementById('hashrate-sub'),
  workers: document.getElementById('workers'),
  workersSub: document.getElementById('workers-sub'),
  height: document.getElementById('height'),
  heightSub: document.getElementById('height-sub'),
  blocks: document.getElementById('blocks'),
  blocksSub: document.getElementById('blocks-sub'),
  marketBar: document.getElementById('market-bar'),
  marketPair: document.getElementById('market-pair'),
  marketPrice: document.getElementById('market-price'),
  marketMeta: document.getElementById('market-meta'),
  workersMeta: document.getElementById('workers-meta'),
  workersBody: document.getElementById('workers-body'),
  events: document.getElementById('events'),
  activityMeta: document.getElementById('activity-meta'),
  stratumPort: document.getElementById('stratum-port'),
};

/* ---------------- theme toggle ---------------- */

const THEME_KEY = 'ntpool-theme';
const darkQuery = window.matchMedia('(prefers-color-scheme: dark)');
const themeToggle = document.getElementById('theme-toggle');

function effectiveTheme() {
  const stored = document.documentElement.dataset.theme;
  if (stored === 'light' || stored === 'dark') return stored;
  return darkQuery.matches ? 'dark' : 'light';
}

function syncThemeLabel() {
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  const label = `Switch to ${next} theme`;
  themeToggle.setAttribute('aria-label', label);
  themeToggle.title = label;
}

themeToggle.addEventListener('click', () => {
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  document.documentElement.dataset.theme = next;
  try {
    localStorage.setItem(THEME_KEY, next);
  } catch {
    /* storage blocked — the choice just won't persist */
  }
  syncThemeLabel();
});

// Follow the OS setting as long as the user has not picked a theme explicitly.
darkQuery.addEventListener('change', () => {
  if (!document.documentElement.dataset.theme) syncThemeLabel();
});

syncThemeLabel();

const state = {
  seeded: false,
  lastHeight: null,
  lastBlocks: null,
  lastWorkerCount: null,
  lastRpc: null,
  lastZmq: null,
};

/* ---------------- formatting ---------------- */

const escapeHtml = (value) =>
  String(value ?? '').replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  }[ch]));

function formatHashrate(value) {
  const h = Number(value) || 0;
  const units = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s'];
  let i = 0;
  let v = h;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i += 1;
  }
  return `${v.toFixed(i === 0 ? 0 : 2)} ${units[i]}`;
}

function formatDifficulty(diff) {
  const d = Number(diff) || 0;
  if (d <= 0) return '—';
  const units = ['', 'K', 'M', 'G', 'T', 'P'];
  let i = 0;
  let v = d;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i += 1;
  }
  return `${v.toFixed(i === 0 ? 2 : 2)}${units[i]}`;
}

function formatUptime(seconds) {
  const s = Number(seconds) || 0;
  if (s <= 0) return '—';
  const m = Math.floor(s / 60);
  const h = Math.floor(m / 60);
  const d = Math.floor(h / 24);
  if (d > 0) return `${d}d ${h % 24}h`;
  if (h > 0) return `${h}h ${m % 60}m`;
  return `${m}m`;
}

function timeAgo(iso) {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '';
  const diff = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  return `${Math.floor(diff / 86400)}d`;
}

function clock(iso) {
  const d = iso ? new Date(iso) : new Date();
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

/* ---------------- activity feed ---------------- */

function pushEvent({ ts, severity = 'info', title, detail }) {
  const placeholder = els.events.querySelector('.event.muted');
  if (placeholder) placeholder.remove();

  const item = document.createElement('li');
  item.className = `event ${severity}`;
  item.innerHTML =
    `<span class="dot"></span>` +
    `<span>${escapeHtml(title)}${detail ? ` <span class="detail">${escapeHtml(detail)}</span>` : ''}</span>` +
    `<time>${escapeHtml(clock(ts))}</time>`;

  els.events.prepend(item);
  while (els.events.children.length > MAX_EVENTS) {
    els.events.lastElementChild.remove();
  }
}

/* ---------------- render ---------------- */

function setPill(el, tone, label) {
  el.className = `pill ${tone}`;
  el.innerHTML = `<i></i>${label}`;
}

function healthTone(health) {
  if (health && health.healthy) return 'ok';
  return 'bad';
}

function renderWorkers(workers = []) {
  if (!workers.length) {
    els.workersBody.innerHTML = '<tr><td colspan="6" class="empty">No miners connected yet.</td></tr>';
    return;
  }

  els.workersBody.innerHTML = workers
    .map((w) => {
      const status = (w.status || 'active').toLowerCase();
      const name = escapeHtml(w.workerName || 'worker');
      const address = escapeHtml(w.address || '');
      const shortAddress = address.length > 18 ? `${address.slice(0, 10)}…${address.slice(-6)}` : address;
      return `<tr>
        <td>${name}<span class="sub">${shortAddress}</span></td>
        <td><span class="state ${status === 'active' ? '' : status}">${escapeHtml(status)}</span></td>
        <td class="num">${formatHashrate(w.hashrate5m)}</td>
        <td class="num">${formatDifficulty(w.difficulty)}</td>
        <td class="num">${(Number(w.acceptedShares) || 0).toLocaleString()} / ${(Number(w.rejectedShares) || 0).toLocaleString()}</td>
        <td class="num">${formatUptime(w.uptimeSeconds)}</td>
      </tr>`;
    })
    .join('');
}

function renderMarket(quote = {}, coinSymbol) {
  const coin = quote.coinSymbol || coinSymbol || 'COIN';
  const vs = quote.quoteSymbol || 'BTC';
  els.marketPair.textContent = `${coin} / ${vs} · BUY`;

  if (quote.lastError && !quote.price) {
    els.marketBar.classList.add('error');
    els.marketPrice.textContent = 'unavailable';
    els.marketMeta.textContent = quote.lastError;
    return;
  }

  els.marketBar.classList.remove('error');
  if (!quote.price) {
    els.marketPrice.textContent = '—';
    els.marketMeta.textContent = 'Waiting for market API';
    return;
  }

  const sats = Number(quote.priceSats) || 0;
  els.marketPrice.textContent = vs.toUpperCase() === 'BTC' && sats > 0
    ? `${sats.toLocaleString()} sats`
    : `${quote.price} ${vs}`;

  const parts = [`1 ${coin} = ${quote.price} ${vs}`];
  if (quote.updatedAt) parts.push(`updated ${timeAgo(quote.updatedAt)} ago`);
  if (quote.lastError) parts.push(`refresh failed: ${quote.lastError}`);
  els.marketMeta.textContent = parts.join(' · ');
}

function render(data) {
  const rpc = data.rpcHealth || { healthy: false };
  const zmq = data.zmqHealth || { healthy: false };
  const poolState = (data.poolHealth && data.poolHealth.overall) || 'degraded';
  const workers = data.workers || [];
  const activeWorkers = workers.filter((w) => (w.status || 'active') === 'active').length;

  els.poolName.textContent = data.poolName || 'ntpool';
  els.network.textContent = data.network || '—';
  els.stratumPort.textContent = data.stratumPort ?? '—';

  els.hashrate.textContent = formatHashrate(data.poolHashrate5m);
  els.hashrateSub.textContent = `1m ${formatHashrate(data.poolHashrate1m)}`;

  els.workers.textContent = String(data.connectedWorkers ?? 0);
  els.workersSub.textContent = `${activeWorkers} active · ${data.activeMiners ?? 0} addresses`;
  els.workersMeta.textContent = `${activeWorkers} active / ${workers.length} sessions`;

  els.height.textContent = Number(data.blockHeight || 0).toLocaleString();
  els.heightSub.textContent = `Diff ${formatDifficulty(data.networkDifficulty)}`;

  els.blocks.textContent = Number(data.blocksFound || 0).toLocaleString();
  els.blocksSub.textContent = Number(data.blocksFound || 0) > 0 ? 'solo blocks credited' : 'none yet';

  setPill(els.pillRpc, healthTone(rpc), rpc.healthy ? 'RPC' : 'RPC down');
  setPill(els.pillZmq, healthTone(zmq), zmq.healthy ? 'ZMQ' : 'ZMQ down');
  setPill(els.pillPool, poolState === 'online' ? 'ok' : 'warn', `Pool ${poolState}`);

  renderMarket(data.marketQuote || {}, data.coinSymbol);
  renderWorkers(workers);
  els.activityMeta.textContent = `updated ${clock()}`;

  /* -------- derive activity events from observed changes -------- */

  if (!state.seeded) {
    state.seeded = true;
    // Newest-first feed: prepend oldest entry first so server order is preserved.
    (data.activityLog || []).slice().reverse().forEach((e) => pushEvent(e));
    state.lastHeight = data.blockHeight ?? null;
    state.lastBlocks = data.blocksFound ?? 0;
    state.lastWorkerCount = data.connectedWorkers ?? 0;
    state.lastRpc = !!rpc.healthy;
    state.lastZmq = !!zmq.healthy;
    return;
  }

  const height = data.blockHeight ?? null;
  if (state.lastHeight !== null && height !== null && height !== state.lastHeight) {
    pushEvent({ severity: 'info', title: `New block height #${height}`, detail: `was #${state.lastHeight}` });
  }
  state.lastHeight = height;

  const blocksFound = Number(data.blocksFound || 0);
  if (state.lastBlocks !== null && blocksFound > state.lastBlocks) {
    pushEvent({ severity: 'success', title: 'Block found', detail: `total ${blocksFound}` });
  }
  state.lastBlocks = blocksFound;

  const workerCount = Number(data.connectedWorkers || 0);
  if (state.lastWorkerCount !== null && workerCount !== state.lastWorkerCount) {
    const delta = workerCount - state.lastWorkerCount;
    pushEvent({
      severity: delta > 0 ? 'success' : 'info',
      title: workerCount > state.lastWorkerCount ? 'Miner connected' : 'Miner disconnected',
      detail: `${workerCount} worker${workerCount === 1 ? '' : 's'} online`,
    });
  }
  state.lastWorkerCount = workerCount;

  if (state.lastRpc !== null && !!rpc.healthy !== state.lastRpc) {
    pushEvent({
      severity: rpc.healthy ? 'success' : 'error',
      title: rpc.healthy ? 'Bitcoin RPC recovered' : 'Bitcoin RPC lost',
      detail: rpc.lastError || '',
    });
  }
  state.lastRpc = !!rpc.healthy;

  if (state.lastZmq !== null && !!zmq.healthy !== state.lastZmq) {
    pushEvent({
      severity: zmq.healthy ? 'success' : 'error',
      title: zmq.healthy ? 'ZMQ feed recovered' : 'ZMQ feed lost',
      detail: zmq.lastError || '',
    });
  }
  state.lastZmq = !!zmq.healthy;
}

/* ---------------- transport ---------------- */

async function loadStatsOnce() {
  try {
    const res = await fetch('/api/stats', { cache: 'no-store' });
    if (!res.ok) return;
    render(await res.json());
  } catch {
    /* the websocket reconnect loop will retry */
  }
}

function connect() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(`${proto}//${location.host}/ws`);
  let retryMs = 1000;

  socket.addEventListener('open', () => {
    retryMs = 1000;
    els.liveDot.classList.add('on');
    els.liveDot.title = 'live';
  });

  socket.addEventListener('message', (event) => {
    try {
      render(JSON.parse(event.data));
    } catch {
      /* ignore malformed frames */
    }
  });

  socket.addEventListener('close', () => {
    els.liveDot.classList.remove('on');
    els.liveDot.title = 'reconnecting';
    setTimeout(connect, retryMs);
    retryMs = Math.min(retryMs * 2, 15000);
  });

  socket.addEventListener('error', () => socket.close());
}

loadStatsOnce();
connect();

// Keep relative timestamps honest even when no new frame arrives.
setInterval(loadStatsOnce, 60000);
