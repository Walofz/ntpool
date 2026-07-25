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

  // Render workers
  const tbody = document.getElementById('workers-tbody');
  if (!data.workers || data.workers.length === 0) {
    tbody.innerHTML = `<tr><td colspan="6" class="text-muted">No active ASIC workers connected. Connect miner to stratum+tcp://localhost:3333</td></tr>`;
  } else {
    tbody.innerHTML = data.workers.map(w => `
      <tr>
        <td class="mono" title="${w.address}">${truncateAddress(w.address)}</td>
        <td><strong>${w.workerName}</strong></td>
        <td class="mono">${w.difficulty}</td>
        <td class="mono">${formatHashrate(w.hashrate1m)}</td>
        <td class="mono">${formatDifficulty(w.bestShareDiff || 0)}</td>
        <td>${w.asicboost ? '<span class="badge badge-asicboost">AsicBoost ON</span>' : '<span class="badge">Standard</span>'}</td>
      </tr>
    `).join('');
  }

  // Render Blocks Mined
  const blocksList = document.getElementById('blocks-list');
  const coinSymbol = data.coinSymbol || 'BTC';
  const addressInput = document.getElementById('address-input');
  if (addressInput && !addressInput.dataset.updated) {
    addressInput.placeholder = `Enter ${coinSymbol} Payout Address`;
    addressInput.dataset.updated = "true";
  }

  if (!data.blocksFound || data.blocksFound.length === 0) {
    blocksList.innerHTML = `<div class="empty-state">No solo blocks found yet. Keep hashing!</div>`;
  } else {
    blocksList.innerHTML = data.blocksFound.map(b => `
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

// Search Miner Address
document.getElementById('search-btn').addEventListener('click', async () => {
  const addr = document.getElementById('address-input').value.trim();
  const container = document.getElementById('miner-result');

  if (!addr) {
    container.classList.add('hidden');
    return;
  }

  try {
    const res = await fetch(`/api/miner/${encodeURIComponent(addr)}`);
    if (!res.ok) {
      container.innerHTML = `<div style="color:#ff5252; padding:0.5rem 0;">Miner address not found or currently offline.</div>`;
      container.classList.remove('hidden');
      return;
    }

    const miner = await res.json();
    container.innerHTML = `
      <div style="background:rgba(0,242,254,0.05); padding:1rem; border-radius:8px; border:1px solid rgba(0,242,254,0.2); margin-top:1rem;">
        <h4 style="color:var(--accent-cyan);">Miner: ${miner.address}</h4>
        <div style="display:grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap:1rem; margin-top:0.8rem;">
          <div><span style="color:#8a99ad; font-size:0.8rem;">Workers:</span> <strong>${miner.workerCount}</strong></div>
          <div><span style="color:#8a99ad; font-size:0.8rem;">Hashrate (1m):</span> <strong>${formatHashrate(miner.hashrate1m)}</strong></div>
          <div><span style="color:#8a99ad; font-size:0.8rem;">Accepted Shares:</span> <strong>${miner.totalAccepted}</strong></div>
          <div><span style="color:#8a99ad; font-size:0.8rem;">Best Share Diff:</span> <strong>${miner.bestShareDiff.toFixed(1)}</strong></div>
        </div>
      </div>
    `;
    container.classList.remove('hidden');
  } catch (e) {
    console.error(e);
  }
});

// Initialize
initWebSocket();
