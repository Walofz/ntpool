const addressInput = document.getElementById('address-input');
const refreshBtn = document.getElementById('refresh-btn');
const autoBtn = document.getElementById('auto-btn');
const statusText = document.getElementById('status-text');
const totalPaidEl = document.getElementById('total-paid');
const balanceEl = document.getElementById('balance');
const unpaidEl = document.getElementById('unpaid');
const jsonOutput = document.getElementById('json-output');

let autoTimer = null;

function getRootPayload(payload) {
  if (payload && typeof payload === 'object' && payload.getuserbalance && typeof payload.getuserbalance === 'object') {
    return payload.getuserbalance;
  }
  return payload || {};
}

function numericValue(root, keys) {
  for (const key of keys) {
    const value = root[key];
    if (value === undefined || value === null) continue;
    const num = Number(value);
    if (Number.isFinite(num)) return num;
  }
  return null;
}

function renderStats(payload) {
  const root = getRootPayload(payload);
  const totalPaid = numericValue(root, ['totalpaid', 'paid', 'total']);
  const balance = numericValue(root, ['balance', 'confirmed']);
  const unpaid = numericValue(root, ['unpaid', 'immature']);

  totalPaidEl.textContent = totalPaid === null ? '-' : totalPaid.toFixed(8);
  balanceEl.textContent = balance === null ? '-' : balance.toFixed(8);
  unpaidEl.textContent = unpaid === null ? '-' : unpaid.toFixed(8);
}

async function loadWalletEx() {
  const address = addressInput.value.trim();
  const query = address ? `?address=${encodeURIComponent(address)}` : '';
  const url = `/api/zpool/walletex${query}`;

  statusText.textContent = 'Loading...';
  statusText.className = 'status';

  try {
    const response = await fetch(url, { cache: 'no-store' });
    const body = await response.text();

    if (!response.ok) {
      throw new Error(`${response.status} ${body}`);
    }

    const json = JSON.parse(body);
    renderStats(json);
    jsonOutput.textContent = JSON.stringify(json, null, 2);
    statusText.textContent = `Updated at ${new Date().toLocaleTimeString()}`;
    statusText.className = 'status ok';
  } catch (error) {
    statusText.textContent = `Error: ${error.message}`;
    statusText.className = 'status err';
  }
}

function toggleAutoRefresh() {
  if (autoTimer) {
    clearInterval(autoTimer);
    autoTimer = null;
    autoBtn.textContent = 'Auto: OFF';
    autoBtn.classList.remove('active');
    return;
  }

  autoTimer = setInterval(loadWalletEx, 30000);
  autoBtn.textContent = 'Auto: ON';
  autoBtn.classList.add('active');
}

refreshBtn.addEventListener('click', loadWalletEx);
autoBtn.addEventListener('click', toggleAutoRefresh);
addressInput.addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    loadWalletEx();
  }
});
loadWalletEx();
