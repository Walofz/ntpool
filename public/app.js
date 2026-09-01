const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${wsProtocol}//${window.location.host}/ws`;

let socket = null;

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
  if (!addr || addr.length < 12) return addr;
  return addr.substring(0, 8) + '...' + addr.substring(addr.length - 6);
}

function formatUptime(seconds) {
  if (!seconds || seconds <= 0) return '0m';
  const mins = Math.floor(seconds / 60);
  const hrs = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hrs > 0) return `${hrs}h ${remMins}m`;
  return `${remMins}m`;
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
}

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

  socket.onclose = () => {
    setTimeout(initWebSocket, 3000);
  };
}

function updateDashboard(data) {
  document.getElementById('pool-hashrate').innerText = formatHashrate(data.poolHashrate1m || 0);
  document.getElementById('connected-workers').innerText = data.connectedWorkers || 0;
  document.getElementById('block-height').innerText = data.blockHeight ? `#${data.blockHeight}` : '0';
  document.getElementById('network-diff').innerText = formatDifficulty(data.networkDifficulty || 0);
  document.getElementById('blocks-count').innerText = data.blocksFound ? data.blocksFound.length : 0;
  document.getElementById('worker-badge').innerText = `${data.connectedWorkers || 0} Online`;

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

  rpcEl.textContent = rpcHealth.healthy ? 'Online' : 'Offline';
  rpcEl.className = rpcHealth.healthy ? 'status-online' : 'status-offline';
  rpcDetail.textContent = rpcHealth.lastError ? rpcHealth.lastError : (rpcHealth.healthy ? 'RPC heartbeat healthy' : 'Waiting for first heartbeat');

  zmqEl.textContent = zmqHealth.healthy ? 'Online' : 'Offline';
  zmqEl.className = zmqHealth.healthy ? 'status-online' : 'status-offline';
  zmqDetail.textContent = zmqHealth.lastError ? zmqHealth.lastError : (zmqHealth.healthy ? 'ZMQ subscription active' : 'Waiting for ZMQ connection');

  poolEl.textContent = poolHealth.overall === 'online' ? 'Online' : 'Degraded';
  poolEl.className = poolHealth.overall === 'online' ? 'status-online' : 'status-offline';

  const alertsList = document.getElementById('alerts-list');
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

  const activityList = document.getElementById('activity-log');
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

  const timelineList = document.getElementById('health-timeline');
  if (!healthTimeline.length) {
    timelineList.innerHTML = `
      <div class="timeline-item neutral">
        <span class="timeline-dot"></span>
        <span>Waiting for health data...</span>
      </div>
    `;
  } else {
    timelineList.innerHTML = [...healthTimeline].reverse().map((entry) => {
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

  document.getElementById('top-worker').innerText = topWorker ? `${topWorker.workerName || 'N/A'} (${formatHashrate(topWorker.hashrate1m || 0)})` : '—';
  document.getElementById('best-share-worker').innerText = bestShareWorker ? `${bestShareWorker.workerName || 'N/A'} (${formatDifficulty(bestShareWorker.bestShareDiff || 0)})` : '—';
  document.getElementById('status-summary').innerText = `${active} / ${disabled} / ${banned}`;
  document.getElementById('reject-rate').innerText = `${rejectRate.toFixed(1)}%`;

  const analyticsList = document.getElementById('worker-analytics-list');
  if (!workers.length) {
    analyticsList.innerHTML = '<div class="analytics-empty">No worker activity yet.</div>';
  } else {
    const ranked = [...workers].sort((a, b) => (b.hashrate1m || 0) - (a.hashrate1m || 0)).slice(0, 5);
    analyticsList.innerHTML = ranked.map((w, idx) => `
      <div class="analytics-item">
        <div class="analytics-head">
          <span>#${idx + 1} ${w.workerName || 'worker'}</span>
          <span class="${statusClass(w.status)}">${(w.status || 'active')}</span>
        </div>
        <div class="analytics-meta">
          <span>${formatHashrate(w.hashrate1m || 0)}</span>
          <span>Best ${formatDifficulty(w.bestShareDiff || 0)}</span>
        </div>
        <div class="analytics-bar">
          <span style="width: ${Math.min(100, ((w.hashrate1m || 0) / (ranked[0].hashrate1m || 1)) * 100)}%"></span>
        </div>
      </div>
    `).join('');
  }

  // Render workers
  const tbody = document.getElementById('workers-tbody');
  if (!data.workers || data.workers.length === 0) {
    tbody.innerHTML = `<tr><td colspan="11" class="text-muted">No active ASIC workers connected. Connect miner to stratum+tcp://localhost:3333</td></tr>`;
  } else {
    tbody.innerHTML = data.workers.map(w => `
      <tr>
        <td class="mono" title="${w.address}">${truncateAddress(w.address)}</td>
        <td><strong>${w.workerName}</strong></td>
        <td><span class="${statusClass(w.status)}">${w.status || 'active'}</span></td>
        <td class="mono">${w.difficulty}</td>
        <td class="mono">${formatHashrate(w.hashrate1m)}</td>
        <td class="mono">${w.acceptedShares || 0}</td>
        <td class="mono">${w.rejectedShares || 0}</td>
        <td class="mono">${formatUptime(w.uptimeSeconds)}</td>
        <td class="mono">${formatDifficulty(w.bestShareDiff || 0)}</td>
        <td>${w.asicboost ? '<span class="badge badge-asicboost">AsicBoost ON</span>' : '<span class="badge">Standard</span>'}</td>
        <td>
          <div class="admin-actions">
            ${w.status === 'active' ? '<button class="mini-btn danger" data-action="disable" data-session-id="' + w.sessionId + '">Disable</button>' : '<button class="mini-btn" data-action="resume" data-session-id="' + w.sessionId + '">Resume</button>'}
            <button class="mini-btn warn" data-action="ban" data-session-id="' + w.sessionId + '">Ban</button>
          </div>
        </td>
      </tr>
    `).join('');

    document.querySelectorAll('[data-action]').forEach((btn) => {
      btn.addEventListener('click', () => {
        handleWorkerAction(btn.dataset.sessionId, btn.dataset.action);
      });
    });
  }

  // Render Blocks Mined
  const blocksList = document.getElementById('blocks-list');
  const coinSymbol = data.coinSymbol || 'BTC';
  const latestBlocks = Array.isArray(data.blocksFound) ? data.blocksFound.slice(0, 3) : [];

  if (latestBlocks.length === 0) {
    blocksList.innerHTML = `<div class="empty-state">No solo blocks found yet. Keep hashing!</div>`;
  } else {
    blocksList.innerHTML = latestBlocks.map(b => `
      <div class="block-card">
        <div class="block-title">
          <span>Block #${b.height}</span>
          <span>+${b.reward} ${b.symbol || coinSymbol}</span>
        </div>
        <div class="block-hash">Hash: ${b.hash}</div>
        <div style="font-size:0.75rem; color:#8a99ad; margin-top:0.3rem;">Mined by: ${truncateAddress(b.miner)}.${b.worker}</div>
      </div>
    `).join('');
  }
}

// Initialize
initWebSocket();
