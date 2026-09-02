const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${wsProtocol}//${window.location.host}/ws`;

let socket = null;
const hashrateTrend = new Map();

function applyTheme(themeName) {
  const theme = themeName === 'modern' ? 'modern' : 'classic';
  document.body.dataset.theme = theme;
  document.querySelectorAll('.theme-button').forEach((button) => {
    button.classList.toggle('active', button.dataset.theme === theme);
  });
  try {
    localStorage.setItem('ntpool-theme', theme);
  } catch (e) {
    // ignore storage issues in restricted environments
  }
}

function initTheme() {
  const savedTheme = (() => {
    try {
      return localStorage.getItem('ntpool-theme');
    } catch (e) {
      return null;
    }
  })();

  const selectedTheme = savedTheme === 'modern' ? 'modern' : 'classic';
  applyTheme(selectedTheme);

  document.querySelectorAll('.theme-button').forEach((button) => {
    button.addEventListener('click', () => applyTheme(button.dataset.theme));
  });
}

function smoothHashrateValue(key, value, alpha = 0.28) {
  const numericValue = Number(value || 0);
  if (!Number.isFinite(numericValue)) return 0;

  const previous = hashrateTrend.get(key);
  if (previous === undefined) {
    hashrateTrend.set(key, numericValue);
    return numericValue;
  }

  const smoothed = previous + alpha * (numericValue - previous);
  hashrateTrend.set(key, smoothed);
  return smoothed;
}

function formatHashrate(hashrate) {
  if (hashrate >= 1e12) return (hashrate / 1e12).toFixed(2) + ' TH/s';
  if (hashrate >= 1e9) return (hashrate / 1e9).toFixed(2) + ' GH/s';
  if (hashrate >= 1e6) return (hashrate / 1e6).toFixed(2) + ' MH/s';
  if (hashrate >= 1e3) return (hashrate / 1e3).toFixed(2) + ' KH/s';
  return hashrate.toFixed(2) + ' H/s';
}

function formatDifficulty(diff) {
  if (!diff || diff <= 0) return '0.00';
  if (diff >= 1e12) return (diff / 1e12).toFixed(2) + ' T';
  if (diff >= 1e9) return (diff / 1e9).toFixed(2) + ' G';
  if (diff >= 1e6) return (diff / 1e6).toFixed(2) + ' M';
  if (diff >= 1e3) return (diff / 1e3).toFixed(2) + ' K';
  return diff.toFixed(2);
}

function truncateAddress(addr) {
  if (!addr) return 'Unknown';
  return addr;
}

function formatUptime(seconds) {
  if (!seconds || seconds <= 0) return '0m';
  const mins = Math.floor(seconds / 60);
  const hrs = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hrs > 0) return `${hrs}h ${remMins}m`;
  return `${remMins}m`;
}

function formatTimeAgo(timestamp) {
  if (!timestamp) return 'Just now';
  const time = new Date(timestamp).getTime();
  if (Number.isNaN(time)) return 'Just now';
  const diffSeconds = Math.max(0, Math.floor((Date.now() - time) / 1000));

  if (diffSeconds < 60) return `${diffSeconds}s ago`;
  const diffMinutes = Math.floor(diffSeconds / 60);
  if (diffMinutes < 60) return `${diffMinutes}m ago`;
  const diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d ago`;
}

function statusClass(status) {
  switch (status) {
    case 'banned': return 'status-tag status-banned';
    case 'disabled': return 'status-tag status-disabled';
    default: return 'status-tag status-active';
  }
}

async function handleWorkerAction(sessionId, action) {
  const reason = action === 'disable' ? 'manual dashboard disable' : action === 'ban' ? 'manual dashboard ban' : 'manual dashboard resume';
  const res = await fetch('/api/admin/worker', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sessionId, action, reason })
  });

  if (!res.ok) {
    alert(`Action failed: ${res.statusText}`);
    return;
  }

  const data = await res.json();
  if (data.ok) {
    const statsRes = await fetch('/api/stats');
    const stats = await statsRes.json();
    updateDashboard(stats);
  }
  closeWorkerActionModal();
}

function setActiveTab(tabName) {
  document.querySelectorAll('.tab-button').forEach((button) => {
    button.classList.toggle('active', button.dataset.tab === tabName);
  });
  document.querySelectorAll('.tab-content').forEach((panel) => {
    panel.classList.toggle('active', panel.id === `tab-${tabName}`);
  });
}

function openWorkerActionModal(worker) {
  const modal = document.getElementById('worker-action-modal');
  const summary = document.getElementById('worker-modal-summary');
  if (!modal || !summary || !worker) return;

  summary.textContent = `${worker.workerName || 'Worker'} • ${worker.address || 'Unknown miner'} • ${worker.status || 'active'}`;
  modal.dataset.sessionId = worker.sessionId;
  modal.classList.remove('hidden');
  modal.setAttribute('aria-hidden', 'false');
}

function closeWorkerActionModal() {
  const modal = document.getElementById('worker-action-modal');
  if (!modal) return;
  modal.classList.add('hidden');
  modal.setAttribute('aria-hidden', 'true');
  delete modal.dataset.sessionId;
}

async function loadInitialStats() {
  try {
    const res = await fetch('/api/stats');
    if (!res.ok) return;
    const data = await res.json();
    updateDashboard(data);
  } catch (e) {
    console.warn('Initial stats fetch failed, waiting for WebSocket data...', e);
  }
}

initTheme();

function initWebSocket() {
  socket = new WebSocket(wsUrl);

  socket.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);
      updateDashboard(data);
    } catch (e) {
      console.error('Failed to parse WS data', e);
    }
  };

  socket.onerror = () => {
    loadInitialStats();
  };

  socket.onclose = () => {
    loadInitialStats();
    setTimeout(initWebSocket, 3000);
  };
}

function updateDashboard(data) {
  const setText = (id, value) => {
    const el = document.getElementById(id);
    if (el) el.innerText = value;
  };

  const displayHashrate = (key, value) => formatHashrate(smoothHashrateValue(key, value));

  const renderStatusBadge = (element, isHealthy, fallbackLabel) => {
    if (!element) return;
    const online = Boolean(isHealthy);
    const label = online ? 'Online' : fallbackLabel;
    const tone = online ? 'online' : 'offline';
    element.className = `health-status ${tone}`;
    element.innerHTML = `<span class="status-indicator ${tone}">${online ? '●' : '■'}</span><span>${label}</span>`;
  };

  setText('pool-hashrate', displayHashrate('pool-hashrate', data.poolHashrate1m || 0));
  setText('connected-workers', data.connectedWorkers || 0);
  setText('block-height', data.blockHeight ? `#${data.blockHeight}` : '0');
  setText('network-diff', formatDifficulty(data.networkDifficulty || 0));
  setText('blocks-count', data.blocksFound ? data.blocksFound.length : 0);
  setText('worker-badge', `${data.connectedWorkers || 0} Online`);

  const rpcHealth = data.rpcHealth || { healthy: false, status: 'offline', lastError: '' };
  const zmqHealth = data.zmqHealth || { healthy: false, status: 'offline', lastError: '' };
  const poolHealth = data.poolHealth || { overall: 'degraded' };
  const alerts = Array.isArray(data.alerts) ? data.alerts : [];
  const activityLog = Array.isArray(data.activityLog) ? data.activityLog : [];
  const healthTimeline = Array.isArray(data.healthTimeline) ? data.healthTimeline : [];

  const rpcEl = document.getElementById('rpc-health');
  const zmqEl = document.getElementById('zmq-health');
  const poolEl = document.getElementById('pool-health');
  const rpcDetail = document.getElementById('rpc-health-detail');
  const zmqDetail = document.getElementById('zmq-health-detail');

  renderStatusBadge(rpcEl, rpcHealth.healthy, 'Offline');
  rpcDetail.textContent = rpcHealth.lastError ? rpcHealth.lastError : (rpcHealth.healthy ? 'RPC heartbeat healthy' : 'Waiting for first heartbeat');

  renderStatusBadge(zmqEl, zmqHealth.healthy, 'Offline');
  zmqDetail.textContent = zmqHealth.lastError ? zmqHealth.lastError : (zmqHealth.healthy ? 'ZMQ subscription active' : 'Waiting for ZMQ connection');

  const poolState = poolHealth.overall === 'online' ? 'online' : (poolHealth.overall === 'degraded' ? 'degraded' : 'offline');
  renderStatusBadge(poolEl, poolState === 'online', poolState === 'degraded' ? 'Degraded' : 'Offline');
  poolEl.setAttribute('data-state', poolState);

  const alertsList = document.getElementById('alerts-list');
  if (alertsList) {
    if (alerts.length === 0) {
      alertsList.innerHTML = `
        <div class="alert-item success">
          <strong>Everything looks healthy.</strong>
          <span>No operational warnings at the moment.</span>
        </div>
      `;
    } else {
      alertsList.innerHTML = alerts.map(alert => `
        <div class="alert-item ${alert.severity || 'warning'}">
          <strong>${alert.title || 'Operational alert'}</strong>
          <span>${alert.detail || 'Check the pool for issues.'}</span>
        </div>
      `).join('');
    }
  }

  const activityList = document.getElementById('activity-log');
  if (activityList) {
    if (!activityLog.length) {
      activityList.innerHTML = `
        <div class="activity-item info">
          <strong>Awaiting activity</strong>
          <span>Pool events will appear here as workers and services change state.</span>
        </div>
      `;
    } else {
      activityList.innerHTML = activityLog.slice(-5).reverse().map(event => {
        const time = new Date(event.ts || Date.now()).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        return `
          <div class="activity-item ${event.severity || 'info'}">
            <strong>${event.title || 'Pool activity'}</strong>
            <span>${event.detail || 'No further details.'} • ${time}</span>
          </div>
        `;
      }).join('');
    }
  }

  const timelineList = document.getElementById('health-timeline');
  if (timelineList) {
    if (!healthTimeline.length) {
      timelineList.innerHTML = `
        <div class="timeline-item neutral">
          <span class="timeline-dot"></span>
          <span>Waiting for health data...</span>
        </div>
      `;
    } else {
      timelineList.innerHTML = [...healthTimeline].reverse().slice(0, 3).map((entry) => {
        const status = entry.overall || 'degraded';
        const icon = status === 'online' ? '●' : status === 'offline' ? '■' : '◐';
        const time = new Date(entry.ts || Date.now()).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        return `
          <div class="timeline-item ${status}">
            <span class="timeline-dot">${icon}</span>
            <span>${status.toUpperCase()} • ${time} • ${entry.connectedWorkers || 0} workers</span>
          </div>
        `;
      }).join('');
    }
  }

  const workers = Array.isArray(data.workers) ? data.workers : [];
  const active = workers.filter(w => (w.status || 'active') === 'active').length;
  const disabled = workers.filter(w => (w.status || 'active') === 'disabled').length;
  const banned = workers.filter(w => (w.status || 'active') === 'banned').length;
  const totalShares = workers.reduce((sum, w) => sum + ((w.acceptedShares || 0) + (w.rejectedShares || 0)), 0);
  const rejectRate = totalShares > 0 ? ((workers.reduce((sum, w) => sum + (w.rejectedShares || 0), 0) / totalShares) * 100) : 0;
  const topWorker = workers.reduce((best, w) => {
    if (!best || (w.hashrate1m || 0) > (best.hashrate1m || 0)) return w;
    return best;
  }, null);
  const bestShareWorker = workers.reduce((best, w) => {
    if (!best || (w.bestShareDiff || 0) > (best.bestShareDiff || 0)) return w;
    return best;
  }, null);

  const topWorkerEl = document.getElementById('top-worker');
  if (topWorkerEl) topWorkerEl.innerText = topWorker ? `${topWorker.workerName || 'N/A'} (${displayHashrate(`worker-${topWorker.sessionId || topWorker.workerName || 'top'}`, topWorker.hashrate1m || 0)})` : '—';

  const bestShareWorkerEl = document.getElementById('best-share-worker');
  if (bestShareWorkerEl) bestShareWorkerEl.innerText = bestShareWorker ? `${bestShareWorker.workerName || 'N/A'} (${formatDifficulty(bestShareWorker.bestShareDiff || 0)})` : '—';

  const statusSummaryEl = document.getElementById('status-summary');
  if (statusSummaryEl) statusSummaryEl.innerText = `${active} / ${disabled} / ${banned}`;

  const rejectRateEl = document.getElementById('reject-rate');
  if (rejectRateEl) rejectRateEl.innerText = `${rejectRate.toFixed(1)}%`;

  const analyticsList = document.getElementById('worker-analytics-list');
  if (analyticsList) {
    if (!workers.length) {
      analyticsList.innerHTML = '<div class="analytics-empty">No worker activity yet.</div>';
    } else {
      const ranked = [...workers].sort((a, b) => (b.hashrate1m || 0) - (a.hashrate1m || 0)).slice(0, 5);
      const totalRate = ranked.reduce((sum, worker) => sum + (worker.hashrate1m || 0), 0) || 1;

      analyticsList.innerHTML = ranked.map((w, idx) => {
        const key = `worker-${w.sessionId || w.workerName || idx}`;
        const displayRate = smoothHashrateValue(key, w.hashrate1m || 0);
        const sharePercent = totalRate > 0 ? (displayRate / totalRate) * 100 : 0;

        return `
          <div class="analytics-item">
            <div class="analytics-head">
              <span>#${idx + 1} ${w.workerName || 'worker'}</span>
              <span class="${statusClass(w.status)}">${(w.status || 'active')}</span>
            </div>
            <div class="analytics-meta">
              <span>${formatHashrate(displayRate)}</span>
              <span>Best ${formatDifficulty(w.bestShareDiff || 0)}</span>
            </div>
            <div class="analytics-bar">
              <span style="width: ${Math.max(8, Math.min(100, sharePercent))}%"></span>
            </div>
          </div>
        `;
      }).join('');
    }
  }

  // Render workers
  const tbody = document.getElementById('workers-tbody');
  if (tbody) {
    if (!data.workers || data.workers.length === 0) {
      tbody.innerHTML = `<tr><td colspan="10" class="text-muted">No active ASIC workers connected. Connect miner to stratum+tcp://localhost:3333</td></tr>`;
    } else {
      tbody.innerHTML = data.workers.map(w => {
        const workerKey = `worker-${w.sessionId || w.workerName || w.address || 'anon'}`;
        const displayRate = smoothHashrateValue(workerKey, w.hashrate1m || 0);
        return `
          <tr class="worker-row" data-session-id="${w.sessionId}" data-worker-name="${(w.workerName || 'worker').replace(/"/g, '&quot;')}" data-worker-address="${(w.address || '').replace(/"/g, '&quot;')}" data-worker-status="${w.status || 'active'}">
            <td class="mono worker-select" title="${w.address || ''}" data-session-id="${w.sessionId}">${truncateAddress(w.address)}</td>
            <td class="worker-select" data-session-id="${w.sessionId}"><strong>${w.workerName || 'worker'}</strong></td>
            <td><span class="${statusClass(w.status)}">${w.status || 'active'}</span></td>
            <td class="mono">${w.difficulty}</td>
            <td class="mono">${formatHashrate(displayRate)}</td>
            <td class="mono">${w.acceptedShares || 0}</td>
            <td class="mono">${w.rejectedShares || 0}</td>
            <td class="mono">${formatUptime(w.uptimeSeconds)}</td>
            <td class="mono">${formatDifficulty(w.bestShareDiff || 0)}</td>
            <td>${w.asicboost ? '<span class="badge badge-asicboost">AsicBoost ON</span>' : '<span class="badge">Standard</span>'}</td>
          </tr>
        `;
      }).join('');

      tbody.querySelectorAll('.worker-select').forEach((cell) => {
        cell.addEventListener('click', (event) => {
          const sessionId = event.currentTarget.dataset.sessionId;
          const worker = (data.workers || []).find((item) => item.sessionId === sessionId);
          if (worker) openWorkerActionModal(worker);
        });
      });
    }
  }

  // Render latest solo block and recent history
  const blocksList = document.getElementById('blocks-list');
  const coinSymbol = data.coinSymbol || 'BTC';
  const recentBlocks = Array.isArray(data.blocksFound) ? data.blocksFound.slice(0, 5) : [];
  const latestBlock = recentBlocks[0];

  if (!latestBlock) {
    blocksList.innerHTML = `<div class="empty-state">No solo blocks found yet. Keep hashing!</div>`;
  } else {
    const blockTimeAgo = formatTimeAgo(latestBlock.timestamp);
    const minerName = latestBlock.worker || 'Unknown worker';
    const minerAddress = latestBlock.miner ? truncateAddress(latestBlock.miner) : 'Unknown miner';
    const recentHistory = recentBlocks.slice(1);

    const historyMarkup = recentHistory.length
      ? `
        <div class="recent-blocks-panel">
          <div class="recent-blocks-header">Recent blocks history</div>
          <div class="recent-block-list">
            ${recentHistory.map(block => `
              <div class="recent-block-item">
                <span>#${block.height}</span>
                <span>${formatTimeAgo(block.timestamp)}</span>
              </div>
            `).join('')}
          </div>
        </div>
      `
      : '';

    blocksList.innerHTML = `
      <div class="block-card latest-block-card">
        <div class="block-title">
          <span>Latest block</span>
          <span>+${latestBlock.reward} ${latestBlock.symbol || coinSymbol}</span>
        </div>

        <div class="block-metric">
          <span class="block-label">Height</span>
          <strong>#${latestBlock.height}</strong>
        </div>

        <div class="block-metric">
          <span class="block-label">Block hash</span>
          <strong>${latestBlock.hash || 'Unknown hash'}</strong>
        </div>

        <div class="block-meta-grid">
          <div>
            <span class="block-label">Found by</span>
            <strong>${minerAddress || 'Unknown miner'} / ${minerName || 'Unknown worker'}</strong>
          </div>
          <div>
            <span class="block-label">Passed</span>
            <strong>${blockTimeAgo}</strong>
          </div>
        </div>
      </div>
      ${historyMarkup}
    `;
  }

  const blockHistoryList = document.getElementById('block-history-list');
  const allBlocks = Array.isArray(data.blocksFound) ? data.blocksFound.slice(0, 10) : [];

  if (!allBlocks.length) {
    blockHistoryList.innerHTML = `<div class="activity-item info"><strong>No discovered blocks yet</strong><span>Block history will appear here once a valid block is found.</span></div>`;
  } else {
    blockHistoryList.innerHTML = `
      <div class="block-history-table-wrap">
        <table class="block-history-table">
          <thead>
            <tr>
              <th>Height</th>
              <th>Hash</th>
              <th>Found By</th>
              <th>Time</th>
            </tr>
          </thead>
          <tbody>
            ${allBlocks.map((block) => `
              <tr>
                <td class="height">#${block.height}</td>
                <td class="hash">${block.hash || 'Unknown hash'}</td>
                <td class="miner">${(block.miner ? truncateAddress(block.miner) : 'Unknown')} / ${block.worker || 'worker'}</td>
                <td class="time">${formatTimeAgo(block.timestamp)}</td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>
    `;
  }
}

document.querySelectorAll('.tab-button').forEach((button) => {
  button.addEventListener('click', () => {
    setActiveTab(button.dataset.tab);
  });
});

document.querySelectorAll('[data-close-modal="true"]').forEach((button) => {
  button.addEventListener('click', closeWorkerActionModal);
});

document.querySelectorAll('[data-worker-action]').forEach((button) => {
  button.addEventListener('click', () => {
    const modal = document.getElementById('worker-action-modal');
    const sessionId = modal ? modal.dataset.sessionId : null;
    if (sessionId) {
      handleWorkerAction(sessionId, button.dataset.workerAction);
    }
  });
});

// Initialize
loadInitialStats();
initWebSocket();
