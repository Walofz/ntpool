import net from 'net';
import { EventEmitter } from 'events';
import { config } from '../config';
import { StratumSession } from './session';
import { JobManager, MiningJob } from '../pool/job';
import { buildCoinbaseTransaction } from '../pool/coinbase';
import {
  sha256d,
  reverseBuffer,
  difficultyToTarget,
  nbitsToTarget,
  hashToDifficulty,
  calculateMerkleRoot,
  calculateAsicBoostVersion,
  buildBlockHeader,
  bufferToBigIntBE,
} from '../crypto/sha256';
import { BitcoinRpcClient } from '../bitcoin/rpc';

export class StratumServer extends EventEmitter {
  private server: net.Server | null = null;
  private sessions: Map<string, StratumSession> = new Map();
  private jobManager: JobManager;
  private bitcoinRpc: BitcoinRpcClient;
  private sessionCounter = 0;

  // Blocks found log
  public foundBlocks: Array<{
    height: number;
    hash: string;
    miner: string;
    worker: string;
    timestamp: Date;
    reward: number;
  }> = [];

  constructor(jobManager: JobManager, bitcoinRpc: BitcoinRpcClient) {
    super();
    this.jobManager = jobManager;
    this.bitcoinRpc = bitcoinRpc;
  }

  public start(): void {
    this.server = net.createServer((socket) => this.handleConnection(socket));
    this.server.listen(config.stratumPort, () => {
      console.log(`[Stratum] Server listening on port ${config.stratumPort}`);
    });
  }

  private handleConnection(socket: net.Socket): void {
    this.sessionCounter++;
    const sessionId = this.sessionCounter.toString(16).padStart(8, '0');
    const extranonce1 = (this.sessionCounter & 0xffffffff).toString(16).padStart(8, '0');

    const session = new StratumSession(sessionId, socket, extranonce1);
    this.sessions.set(sessionId, session);

    let buffer = '';

    socket.on('data', (data) => {
      buffer += data.toString('utf8');
      const lines = buffer.split('\n');
      buffer = lines.pop() || ''; // Keep incomplete trailing line

      for (const line of lines) {
        if (line.trim().length > 0) {
          this.handleMessage(session, line.trim());
        }
      }
    });

    socket.on('close', () => {
      this.sessions.delete(sessionId);
      this.emit('stats_updated');
    });

    socket.on('error', (err) => {
      socket.destroy();
      this.sessions.delete(sessionId);
    });
  }

  private sendResponse(session: StratumSession, id: any, result: any, error: any = null): void {
    const payload = JSON.stringify({ id, result, error }) + '\n';
    session.socket.write(payload);
  }

  private sendNotification(session: StratumSession, method: string, params: any[]): void {
    const payload = JSON.stringify({ id: null, method, params }) + '\n';
    session.socket.write(payload);
  }

  private handleMessage(session: StratumSession, rawLine: string): void {
    let msg: any;
    try {
      msg = JSON.parse(rawLine);
    } catch (e) {
      return this.sendResponse(session, null, null, { code: -32700, message: 'Parse error' });
    }

    const { id, method, params } = msg;

    switch (method) {
      case 'mining.configure':
        this.handleConfigure(session, id, params);
        break;
      case 'mining.subscribe':
        this.handleSubscribe(session, id, params);
        break;
      case 'mining.authorize':
        this.handleAuthorize(session, id, params);
        break;
      case 'mining.submit':
        this.handleSubmit(session, id, params);
        break;
      case 'mining.extranonce.subscribe':
      case 'mining.suggest_difficulty':
      case 'mining.suggest_target':
        this.sendResponse(session, id, true);
        break;
      default:
        this.sendResponse(session, id, null, { code: -32601, message: 'Method not found' });
        break;
    }
  }

  /**
   * Handle AsicBoost Negotiation (mining.configure)
   */
  private handleConfigure(session: StratumSession, id: any, params: any[]): void {
    const extensions = params[0] || [];
    const extensionParams = params[1] || {};

    const result: Record<string, any> = {};

    if (extensions.includes('version-rolling')) {
      session.versionRollingEnabled = true;
      session.versionRollingMask = extensionParams['version-rolling.mask'] || '1fffe000';
      session.versionRollingMinBitCount = extensionParams['version-rolling.min-bit-count'] || 16;

      result['version-rolling'] = true;
      result['version-rolling.mask'] = session.versionRollingMask;
    }

    this.sendResponse(session, id, result);
  }

  /**
   * Handle mining.subscribe
   */
  private handleSubscribe(session: StratumSession, id: any, params: any[]): void {
    session.isSubscribed = true;

    const subscriptions = [
      ['mining.set_difficulty', `${session.id}_diff`],
      ['mining.notify', `${session.id}_notify`],
    ];

    this.sendResponse(session, id, [subscriptions, session.extranonce1, session.extranonce2Size]);

    // Send initial set_difficulty
    this.sendNotification(session, 'mining.set_difficulty', [session.currentDiff]);
  }

  /**
   * Handle mining.authorize
   */
  private handleAuthorize(session: StratumSession, id: any, params: any[]): void {
    const fullUser = params[0] || 'unknown';
    const userParts = fullUser.split('.');
    session.minerAddress = userParts[0];
    session.workerName = userParts[1] || 'default';
    session.isAuthorized = true;

    this.sendResponse(session, id, true);
    this.emit('stats_updated');

    // Send current job
    const currentJob = this.jobManager.getCurrentJob();
    if (currentJob) {
      this.sendJobToSession(session, currentJob, true);
    } else {
      console.warn(`[Stratum Warning] Worker ${session.minerAddress}.${session.workerName} authorized, but no active block template available from Bitcoin Node yet!`);
    }
  }

  /**
   * Handle share submission (mining.submit)
   */
  private async handleSubmit(session: StratumSession, id: any, params: any[]): Promise<void> {
    if (!session.isAuthorized) {
      return this.sendResponse(session, id, false, { code: 24, message: 'Unauthorized worker' });
    }

    const workerName = params[0];
    const jobId = params[1];
    const extranonce2Hex = params[2];
    const nTimeHex = params[3];
    const nonceHex = params[4];
    const versionBitsHex = params[5]; // AsicBoost version_bits (optional)

    const job = this.jobManager.getJob(jobId);
    if (!job) {
      session.rejectedShares++;
      return this.sendResponse(session, id, false, { code: 21, message: 'Stale / Job not found' });
    }

    // 1. Build Coinbase Tx for this miner
    const coinbase = buildCoinbaseTransaction({
      blockHeight: job.blockHeight,
      coinbaseValue: job.coinbaseValue,
      minerAddress: session.minerAddress,
      extranonce1Size: 4,
      extranonce2Size: session.extranonce2Size,
      defaultWitnessCommitment: job.defaultWitnessCommitment,
    });

    // Reconstruct full coinbase tx
    const coinbaseTxHex = `${coinbase.coinb1}${session.extranonce1}${extranonce2Hex}${coinbase.coinb2}`;
    const coinbaseTxBuf = Buffer.from(coinbaseTxHex, 'hex');

    // Coinbase TxID (SHA-256d)
    const coinbaseTxIdLE = sha256d(coinbaseTxBuf);

    // 2. Calculate Merkle Root
    const merkleRootBE = calculateMerkleRoot(coinbaseTxIdLE, job.merkleBranchHex);

    // 3. AsicBoost Header Version
    const version = calculateAsicBoostVersion(
      job.versionHex,
      versionBitsHex,
      session.versionRollingMask
    );

    // 4. Targets & Multi-pass Header Endianness Verification
    const minerTarget = difficultyToTarget(session.currentDiff);
    const networkTarget = nbitsToTarget(job.nBitsHex);

    const combinations = [
      { swapNonce: false, swapNtime: false },
      { swapNonce: true, swapNtime: false },
      { swapNonce: false, swapNtime: true },
      { swapNonce: true, swapNtime: true },
    ];

    let bestHeader = buildBlockHeader({
      version,
      prevHashRawHex: job.prevHashRaw,
      merkleRootBE,
      nTimeHex,
      nBitsHex: job.nBitsHex,
      nonceHex,
    });
    let bestHashLE = sha256d(bestHeader);
    let bestHashBE = reverseBuffer(bestHashLE);
    let bestHashBigInt = bufferToBigIntBE(bestHashBE);
    let bestShareDiff = hashToDifficulty(bestHashLE);

    for (const combo of combinations) {
      const h = buildBlockHeader({
        version,
        prevHashRawHex: job.prevHashRaw,
        merkleRootBE,
        nTimeHex,
        nBitsHex: job.nBitsHex,
        nonceHex,
        swapNonceByteOrder: combo.swapNonce,
        swapNtimeByteOrder: combo.swapNtime,
      });
      const hLE = sha256d(h);
      const hBE = reverseBuffer(hLE);
      const hBigInt = bufferToBigIntBE(hBE);
      const diff = hashToDifficulty(hLE);

      if (hBigInt <= minerTarget) {
        bestHeader = h;
        bestHashLE = hLE;
        bestHashBE = hBE;
        bestHashBigInt = hBigInt;
        bestShareDiff = diff;
        break;
      }

      if (diff > bestShareDiff) {
        bestHeader = h;
        bestHashLE = hLE;
        bestHashBE = hBE;
        bestHashBigInt = hBigInt;
        bestShareDiff = diff;
      }
    }

    const header = bestHeader;
    const headerHashLE = bestHashLE;
    const headerHashBE = bestHashBE;
    const hashBigInt = bestHashBigInt;
    const shareDiff = bestShareDiff;

    // Check against miner difficulty
    if (hashBigInt > minerTarget) {
      session.rejectedShares++;
      console.log(`[Share Rejected] Worker: ${session.workerName}, Achieved Diff: ${shareDiff.toFixed(4)}, Required Diff: ${session.currentDiff}`);
      return this.sendResponse(session, id, false, {
        code: 23,
        message: `Low difficulty share (Achieved diff ${shareDiff.toFixed(2)} < required ${session.currentDiff})`,
      });
    }

    console.log(`[Share ACCEPTED] Worker: ${session.workerName}, Achieved Diff: ${shareDiff.toFixed(2)}, Required Diff: ${session.currentDiff}`);

    // ACCEPTED SHARE!
    const newDiff = session.recordShare(shareDiff);
    this.sendResponse(session, id, true);
    this.emit('stats_updated');

    // Send updated difficulty if Vardiff changed
    if (newDiff !== null) {
      this.sendNotification(session, 'mining.set_difficulty', [newDiff]);
    }

    // CHECK IF THIS SHARE FOUND A BLOCK FOR THE NETWORK! 🎉
    if (hashBigInt <= networkTarget) {
      console.log(`[BLOCK FOUND] 🎉🎉🎉 Miner ${session.minerAddress} FOUND BLOCK #${job.blockHeight}!`);
      console.log(`Block Hash: ${headerHashBE.toString('hex')}`);

      const blockHex = this.buildFullBlockHex(header, coinbaseTxHex, job.txsData);
      
      try {
        const result = await this.bitcoinRpc.submitBlock(blockHex);
        console.log(`[RPC submitblock] Result: ${result === null ? 'SUCCESS (ACCEPTED)' : result}`);

        this.foundBlocks.unshift({
          height: job.blockHeight,
          hash: headerHashBE.toString('hex'),
          miner: session.minerAddress,
          worker: session.workerName,
          timestamp: new Date(),
          reward: job.coinbaseValue / 1e8,
        });
        this.emit('block_found', this.foundBlocks[0]);
      } catch (err: any) {
        console.error(`[RPC submitblock Error]:`, err.message);
      }
    }
  }

  /**
   * Combine 80-byte header + tx count + coinbase tx + block transactions into full block hex
   */
  private buildFullBlockHex(header: Buffer, coinbaseTxHex: string, txsData: string[]): string {
    const txCount = 1 + txsData.length;
    const txCountBuf = Buffer.from([txCount]);

    let hex = header.toString('hex');
    hex += txCountBuf.toString('hex');
    hex += coinbaseTxHex;

    for (const txData of txsData) {
      hex += txData;
    }

    return hex;
  }

  public broadcastJob(job: MiningJob, cleanJobs = true): void {
    for (const session of this.sessions.values()) {
      if (session.isAuthorized) {
        this.sendJobToSession(session, job, cleanJobs);
      }
    }
  }

  private sendJobToSession(session: StratumSession, job: MiningJob, cleanJobs: boolean): void {
    const coinbase = buildCoinbaseTransaction({
      blockHeight: job.blockHeight,
      coinbaseValue: job.coinbaseValue,
      minerAddress: session.minerAddress,
      extranonce1Size: 4,
      extranonce2Size: session.extranonce2Size,
      defaultWitnessCommitment: job.defaultWitnessCommitment,
    });

    const params = [
      job.jobId,
      job.prevHashStratum,
      coinbase.coinb1,
      coinbase.coinb2,
      job.merkleBranchHex,
      job.versionHex,
      job.nBitsHex,
      job.nTimeHex,
      cleanJobs,
    ];

    this.sendNotification(session, 'mining.notify', params);
  }

  public getActiveSessions(): StratumSession[] {
    return Array.from(this.sessions.values());
  }
}
