import net from 'net';
import crypto from 'crypto';

const STRATUM_PORT = 3333;
const BTC_ADDRESS = '1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa';
const WORKER_NAME = 'antminer_s19_sim';

console.log(`[Simulator Miner] Connecting to Stratum server localhost:${STRATUM_PORT}...`);

const socket = net.connect(STRATUM_PORT, '127.0.0.1', () => {
  console.log('[Simulator Miner] Connected! Sending mining.configure (AsicBoost)...');

  // 1. AsicBoost Handshake
  socket.write(
    JSON.stringify({
      id: 1,
      method: 'mining.configure',
      params: [
        ['version-rolling'],
        {
          'version-rolling.mask': '1fffe000',
          'version-rolling.min-bit-count': 16,
        },
      ],
    }) + '\n'
  );
});

let currentJob: any = null;
let currentDiff = 1;
let extranonce1 = '';
let extranonce2Size = 4;

let msgBuffer = '';

socket.on('data', (data) => {
  msgBuffer += data.toString('utf8');
  const lines = msgBuffer.split('\n');
  msgBuffer = lines.pop() || '';

  for (const line of lines) {
    if (!line.trim()) continue;
    const msg = JSON.parse(line.trim());

    // Response to configure
    if (msg.id === 1) {
      console.log('[Simulator Miner] AsicBoost Configured:', JSON.stringify(msg.result));
      // 2. Subscribe
      socket.write(
        JSON.stringify({
          id: 2,
          method: 'mining.subscribe',
          params: ['AntminerSim/1.0.0'],
        }) + '\n'
      );
    }

    // Response to subscribe
    if (msg.id === 2) {
      extranonce1 = msg.result[1];
      extranonce2Size = msg.result[2];
      console.log(`[Simulator Miner] Subscribed! Extranonce1: ${extranonce1}, Extranonce2Size: ${extranonce2Size}`);

      // 3. Authorize
      socket.write(
        JSON.stringify({
          id: 3,
          method: 'mining.authorize',
          params: [`${BTC_ADDRESS}.${WORKER_NAME}`, 'x'],
        }) + '\n'
      );
    }

    // Response to authorize
    if (msg.id === 3) {
      console.log('[Simulator Miner] Authorized successfully! Waiting for jobs...');
    }

    // Notifications
    if (msg.method === 'mining.set_difficulty') {
      currentDiff = msg.params[0];
      console.log(`[Simulator Miner] Difficulty set to: ${currentDiff}`);
    }

    if (msg.method === 'mining.notify') {
      currentJob = {
        jobId: msg.params[0],
        prevHash: msg.params[1],
        coinb1: msg.params[2],
        coinb2: msg.params[3],
        merkleBranch: msg.params[4],
        version: msg.params[5],
        nbits: msg.params[6],
        ntime: msg.params[7],
        cleanJobs: msg.params[8],
      };
      console.log(`[Simulator Miner] Received New Job #${currentJob.jobId}! Mining shares...`);
      mineAndSubmitShares();
    }
  }
});

function mineAndSubmitShares() {
  if (!currentJob) return;

  // Simulate hashing 10 shares
  for (let i = 0; i < 5; i++) {
    setTimeout(() => {
      const extranonce2 = crypto.randomBytes(extranonce2Size).toString('hex');
      const nonce = crypto.randomBytes(4).toString('hex');
      const versionBits = '00002000'; // Modified AsicBoost version bits

      const submitPayload = {
        id: 4 + i,
        method: 'mining.submit',
        params: [
          `${BTC_ADDRESS}.${WORKER_NAME}`,
          currentJob.jobId,
          extranonce2,
          currentJob.ntime,
          nonce,
          versionBits,
        ],
      };

      console.log(`[Simulator Miner] Submitting share (Nonce: ${nonce}, AsicBoost VersionBits: ${versionBits})...`);
      socket.write(JSON.stringify(submitPayload) + '\n');
    }, i * 800);
  }
}
