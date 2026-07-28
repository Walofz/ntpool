function getNumeric(source, keys) {
  if (!source || typeof source !== 'object') {
    return '-';
  }

  for (const key of keys) {
    const value = source[key];
    if (value !== undefined && value !== null && value !== '') {
      return value;
    }
  }

  return '-';
}

function prettyPrint(targetId, data) {
  const target = document.getElementById(targetId);
  target.textContent = JSON.stringify(data, null, 2);
}

function readWalletRoot(walletResponse) {
  if (walletResponse && typeof walletResponse === 'object' && walletResponse.getuserbalance) {
    return walletResponse.getuserbalance;
  }
  return walletResponse;
}

async function fetchJson(path) {
  const response = await fetch(path, {
    headers: {
      'Accept': 'application/json'
    }
  });

  if (!response.ok) {
    const body = await response.text();
    throw new Error(`${path} failed (${response.status}): ${body}`);
  }

  return response.json();
}

async function refreshDashboard() {
  const refreshButton = document.getElementById('refresh-btn');
  refreshButton.disabled = true;
  refreshButton.textContent = 'Refreshing...';

  try {
    const [statusData, walletData, currenciesData] = await Promise.all([
      fetchJson('/api/zpool/status'),
      fetchJson('/api/zpool/wallet'),
      fetchJson('/api/zpool/currencies')
    ]);

    prettyPrint('status-json', statusData);
    prettyPrint('wallet-json', walletData);
    prettyPrint('currencies-json', currenciesData);

    const walletRoot = readWalletRoot(walletData);

    document.getElementById('status-hashrate').textContent = getNumeric(statusData, ['hashrate', 'hashrate_shared']);
    document.getElementById('status-miners').textContent = getNumeric(statusData, ['miners']);
    document.getElementById('status-workers').textContent = getNumeric(statusData, ['workers']);
    document.getElementById('wallet-immature').textContent = getNumeric(walletRoot, ['immature', 'unconfirmed']);
    document.getElementById('wallet-balance').textContent = getNumeric(walletRoot, ['balance', 'confirmed']);
    document.getElementById('wallet-paid').textContent = getNumeric(walletRoot, ['paid24h', 'totalpaid']);
  } catch (error) {
    document.getElementById('status-json').textContent = error.message;
    document.getElementById('wallet-json').textContent = error.message;
    document.getElementById('currencies-json').textContent = error.message;
  } finally {
    refreshButton.disabled = false;
    refreshButton.textContent = 'Refresh';
  }
}

document.getElementById('refresh-btn').addEventListener('click', refreshDashboard);
refreshDashboard();
