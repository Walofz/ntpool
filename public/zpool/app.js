function fmt8(val) {
  const n = parseFloat(val);
  if (isNaN(n)) return '-';
  return n.toFixed(8);
}

function timeAgo(unixTs) {
  const now = Math.floor(Date.now() / 1000);
  const diff = now - unixTs;
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

function truncateTx(tx) {
  if (!tx || tx.length < 16) return tx || '-';
  return tx.substring(0, 10) + '...' + tx.substring(tx.length - 8);
}

async function fetchWalletEx() {
  const resp = await fetch('/api/zpool/walletex', { headers: { Accept: 'application/json' } });
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`walletex failed (${resp.status}): ${body}`);
  }
  return resp.json();
}

function renderStats(data) {
  document.getElementById('stat-unpaid').textContent  = fmt8(data.unpaid);
  document.getElementById('stat-unsold').textContent  = fmt8(data.unsold);
  document.getElementById('stat-balance').textContent = fmt8(data.balance);
  document.getElementById('stat-paid24h').textContent = fmt8(data.paid24h);
  document.getElementById('stat-total').textContent   = fmt8(data.total);
}

function renderMiners(miners) {
  const tbody = document.getElementById('miners-tbody');
  const badge = document.getElementById('miner-badge');
  const list = Array.isArray(miners) ? miners : [];
  badge.textContent = `${list.length} Online`;

  if (list.length === 0) {
    tbody.innerHTML = `<tr><td colspan="5" class="text-muted">No miners connected</td></tr>`;
    return;
  }

  tbody.innerHTML = list.map(m => `
    <tr>
      <td><span class="algo-badge">${m.algo || '-'}</span></td>
      <td>${m.difficulty ?? '-'}</td>
      <td>${(m.accepted ?? 0).toFixed(2)}</td>
      <td>${m.rejected ?? 0}</td>
      <td style="font-size:0.8rem;color:var(--text-muted)">${m.version || '-'}</td>
    </tr>
  `).join('');
}

function renderPayouts(payouts) {
  const container = document.getElementById('payouts-list');
  const list = Array.isArray(payouts) ? payouts.slice().reverse().slice(0, 10) : [];

  if (list.length === 0) {
    container.innerHTML = `<div class="empty-state">No payouts yet</div>`;
    return;
  }

  container.innerHTML = list.map(p => `
    <div class="payout-card">
      <div class="payout-title">
        <span>+${p.amount} BTC</span>
        <span class="payout-time">${timeAgo(p.time)}</span>
      </div>
      <div class="payout-tx">TX: ${truncateTx(p.tx)}</div>
    </div>
  `).join('');
}

async function refresh() {
  const btn = document.getElementById('refresh-btn');
  btn.disabled = true;
  btn.textContent = '⟳ Refreshing...';

  try {
    const data = await fetchWalletEx();
    renderStats(data);
    renderMiners(data.miners);
    renderPayouts(data.payouts);
    document.getElementById('last-updated').textContent =
      'Updated ' + new Date().toLocaleTimeString();
  } catch (err) {
    document.getElementById('last-updated').textContent = 'Error: ' + err.message;
  } finally {
    btn.disabled = false;
    btn.textContent = '⟳ Refresh';
  }
}

document.getElementById('refresh-btn').addEventListener('click', refresh);

refresh();
setInterval(refresh, 60000);
