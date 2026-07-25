import { config } from './config';
import { BitcoinRpcClient } from './bitcoin/rpc';
import { JobManager } from './pool/job';
import { StratumServer } from './stratum/server';
import { BlockNotifier } from './bitcoin/zmq';
import { WebDashboardServer } from './web/server';

console.log('====================================================');
console.log(` ⚡ ${config.poolName}`);
console.log(' Modelled after CKPool Architecture (Solo Mining)');
console.log(' Overt AsicBoost (Version Rolling) Enabled');
console.log('====================================================');

async function main() {
  const bitcoinRpc = new BitcoinRpcClient();
  const jobManager = new JobManager();
  const stratumServer = new StratumServer(jobManager, bitcoinRpc);
  const blockNotifier = new BlockNotifier(bitcoinRpc, jobManager, stratumServer);
  const webDashboard = new WebDashboardServer(stratumServer, jobManager);

  // 1. Start Stratum TCP Server
  stratumServer.start();

  // 2. Start Web Dashboard
  webDashboard.start();

  // 3. Start Bitcoin Node Poller / Block Notifier
  await blockNotifier.start();

  console.log(`\n[Pool Ready] Stratum: stratum+tcp://localhost:${config.stratumPort}`);
  console.log(`[Pool Ready] Web UI:  http://localhost:${config.webPort}`);
}

main().catch((err) => {
  console.error('[Fatal Error] Failed to launch pool:', err.message);
  process.exit(1);
});
