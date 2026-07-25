import net from 'net';
import fs from 'fs';
import path from 'path';
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

export interface FoundBlock {
  height: number;
  hash: string;
  miner: string;
  worker: string;
  timestamp: Date | string;
  reward: number;
  symbol: string;
}

export class StratumServer extends EventEmitter {
  private server: net.Server | null = null;
  private sessions: Map<string, StratumSession> = new Map();
  private jobManager: JobManager;
  private bitcoinRpc: BitcoinRpcClient;
  private sessionCounter = 0;
  private blocksFilePath = path.join(process.cwd(), 'data', 'found_blocks.json');

  // Blocks found log
  public foundBlocks: FoundBlock[] = [];

  constructor(jobManager: JobManager, bitcoinRpc: BitcoinRpcClient) {
    super();
    this.jobManager = jobManager;
    this.bitcoinRpc = bitcoinRpc;
    this.loadFoundBlocks();
  }

  private loadFoundBlocks(): void {
    try {
      const dir = path.dirname(this.blocksFilePath);
      if (!fs.existsSync(dir)) {
        fs.mkdirSync(dir, { recursive: true });
      }

      if (fs.existsSync(this.blocksFilePath)) {
        const raw = fs.readFileSync(this.blocksFilePath, 'utf8');
        const data = JSON.parse(raw);
        if (Array.isArray(data)) {
          this.foundBlocks = data.map((b: any) => ({
            ...b,
            symbol: b.symbol || config.coinSymbol,
          }));
          console.log(`[Stratum] Loaded ${this.foundBlocks.length} persisted found blocks from disk.`);
        }
      }
    } catch (err: any) {
      console.error('[Stratum Error] Failed to load found_blocks.json:', err.message);
    }
  }

  private saveFoundBlocks(): void {
    try {
      const dir = path.dirname(this.blocksFilePath);
      if (!fs.existsSync(dir)) {
        fs.mkdirSync(dir, { recursive: true });
      }
      fs.writeFileSync(this.blocksFilePath, JSON.stringify(this.foundBlocks, null, 2), 'utf8');
    } catch (err: any) {
      console.error('[Stratum Error] Failed to save found_blocks.json:', err.message);
    }
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
        this.handleSuggestDifficulty(session, id, params);
        break;
      case 'mining.extranonce.subscribe':
        this.sendResponse(session, id, true);
        break;
      default:
        this.sendResponse(session, id, null, { code: -32601, message: 'Method not found' });
        break;
    }
  }

  /**
   * Handle mining.suggest_difficulty
   */
  private handleSuggestDifficulty(session: StratumSession, id: any, params: any[]): void {
    const suggested = parseFloat(params[0]);
    if (!isNaN(suggested) && suggested > 0) {
      const clamped = Math.max(config.minDiff, Math.min(config.maxDiff, suggested));
      session.currentDiff = clamped;
      this.sendNotification(session, 'mining.set_difficulty', [session.currentDiff]);
    }
    this.sendResponse(session, id, true);
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
    const password = params[1] || '';

    const userParts = fullUser.split('.');
    session.minerAddress = userParts[0];
    session.workerName = userParts[1] || 'default';
    session.isAuthorized = true;

    // Check for custom difficulty request (e.g. d=1 or d=64 or d=512 in password/user)
    const diffMatch = (fullUser + ' ' + password).match(/(?:^|[\s,;+])d=([0-9.]+)/i);
    if (diffMatch && diffMatch[1]) {
      const requestedDiff = parseFloat(diffMatch[1]);
      if (!isNaN(requestedDiff) && requestedDiff > 0) {
        session.currentDiff = Math.max(config.minDiff, Math.min(config.maxDiff, requestedDiff));
        this.sendNotification(session, 'mining.set_difficulty', [session.currentDiff]);
      }
    }

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

    const currentJob = this.jobManager.getCurrentJob();
    // Only mark as stale if block height has changed on the network
    if (currentJob && job.blockHeight !== currentJob.blockHeight) {
      session.rejectedShares++;
      return this.sendResponse(session, id, false, { code: 21, message: 'Stale block height' });
    }

    // Default coinbase for block submission (will be updated if ext2 candidate matches)
    const coinbaseTxHex = `${job.coinb1}${session.extranonce1}${extranonce2Hex}${job.coinb2}`;

    // Generate version candidates covering AsicBoost (BIP310 / overt AsicBoost / Canaan / LE versionBits)
    const versionCandidates = this.getVersionCandidates(job.versionHex, versionBitsHex, session.versionRollingMask);

    // Targets
    const minerTarget = difficultyToTarget(session.currentDiff);
    const networkTarget = job.targetHex ? bufferToBigIntBE(Buffer.from(job.targetHex, 'hex')) : nbitsToTarget(job.nBitsHex);

    let finalHeader: Buffer = Buffer.alloc(80);
    let finalHashLE: Buffer = Buffer.alloc(32);
    let finalHashBE: Buffer = Buffer.alloc(32);
    let finalHashBigInt = 0n;
    let finalShareDiff = 0;
    let accepted = false;
    let matchedCoinbaseTxHex = coinbaseTxHex;

    // Generate extranonce2 candidates (original + byte-reversed for Avalon/Canaan)
    const ext2Candidates = this.getExt2Candidates(extranonce2Hex, session.extranonce2Size);

    // Generate nonce candidates: standard (BE hex→uint32) + reversed (LE hex for Avalon Nano 3)
    const nonceRevBuf = Buffer.from(nonceHex.padStart(8, '0'), 'hex');
    const nonceReversedHex = reverseBuffer(nonceRevBuf).toString('hex');
    const nonceCandidates = [nonceHex];
    if (nonceReversedHex !== nonceHex) nonceCandidates.push(nonceReversedHex);

    // Evaluate all version × ext2 × nonce combinations
    primaryLoop: for (const ext2 of ext2Candidates) {
      const cbTxHex = `${job.coinb1}${session.extranonce1}${ext2}${job.coinb2}`;
      const cbTxIdLE = sha256d(Buffer.from(cbTxHex, 'hex'));
      const mRoot = calculateMerkleRoot(cbTxIdLE, job.merkleBranchHex);

      for (const ver of versionCandidates) {
        for (const nonce of nonceCandidates) {
          const header = buildBlockHeader({
            version: ver,
            prevHashRawHex: job.prevHashRaw,
            merkleRootBE: mRoot,
            nTimeHex,
            nBitsHex: job.nBitsHex,
            nonceHex: nonce,
          });
          const headerHashLE = sha256d(header);
          const headerHashBE = reverseBuffer(headerHashLE);
          const hashBigInt = bufferToBigIntBE(headerHashBE);

          if (hashBigInt <= minerTarget) {
            finalHeader = header;
            finalHashLE = headerHashLE;
            finalHashBE = headerHashBE;
            finalHashBigInt = hashBigInt;
            finalShareDiff = hashToDifficulty(headerHashLE);
            accepted = true;
            matchedCoinbaseTxHex = cbTxHex;
            break primaryLoop;
          }

          const candDiff = hashToDifficulty(headerHashLE);
          if (candDiff > finalShareDiff) {
            finalHeader = header;
            finalHashLE = headerHashLE;
            finalHashBE = headerHashBE;
            finalHashBigInt = hashBigInt;
            finalShareDiff = candDiff;
          }
        }
      }
    }

    if (!accepted) {
      session.rejectedShares++;
      return this.sendResponse(session, id, false, {
        code: 23,
        message: `Low difficulty share (Achieved diff ${finalShareDiff.toFixed(2)} < required ${session.currentDiff})`,
      });
    }

    console.log(`[Share ACCEPTED] Worker: ${session.workerName}, Achieved Diff: ${finalShareDiff.toFixed(2)}, Required Diff: ${session.currentDiff}`);

    // ACCEPTED SHARE!
    const newDiff = session.recordShare(session.currentDiff, finalShareDiff);
    this.sendResponse(session, id, true);
    this.emit('stats_updated');

    // Send updated difficulty if Vardiff changed
    if (newDiff !== null) {
      this.sendNotification(session, 'mining.set_difficulty', [newDiff]);
    }

    // CHECK IF THIS SHARE FOUND A BLOCK FOR THE NETWORK! 🎉
    if (finalHashBigInt <= networkTarget) {
      console.log(`[BLOCK FOUND] 🎉🎉🎉 Miner ${session.minerAddress} FOUND BLOCK #${job.blockHeight}!`);
      console.log(`Block Hash: ${finalHashBE.toString('hex')}`);

      this.resetAllBestShares();

      const blockHex = this.buildFullBlockHex(finalHeader, matchedCoinbaseTxHex, job.txsData);

      try {
        const result = await this.bitcoinRpc.submitBlock(blockHex);
        console.log(`[RPC submitblock] Result: ${result === null ? 'SUCCESS (ACCEPTED)' : result}`);

        const blockRecord: FoundBlock = {
          height: job.blockHeight,
          hash: finalHashBE.toString('hex'),
          miner: session.minerAddress,
          worker: session.workerName,
          timestamp: new Date(),
          reward: job.coinbaseValue / 1e8,
          symbol: config.coinSymbol,
        };

        this.foundBlocks.unshift(blockRecord);
        this.saveFoundBlocks();
        this.emit('block_found', blockRecord);
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

  private lastBroadcastHeight = 0;

  public resetAllBestShares(): void {
    for (const session of this.sessions.values()) {
      session.resetBestShare();
    }
    console.log(`[Pool] 🎉 Solo Block Found! Resetting Best Share to 0 for all workers.`);
    this.emit('stats_updated');
  }

  public broadcastJob(job: MiningJob, cleanJobs = true): void {
    this.lastBroadcastHeight = job.blockHeight;

    for (const session of this.sessions.values()) {
      if (session.isAuthorized) {
        this.sendJobToSession(session, job, cleanJobs);
      }
    }
  }

  private sendJobToSession(session: StratumSession, job: MiningJob, cleanJobs: boolean): void {
    const params = [
      job.jobId,
      job.prevHashStratum,
      job.coinb1,
      job.coinb2,
      job.merkleBranchHex,
      job.versionHex,
      job.nBitsHex,
      job.nTimeHex,
      cleanJobs,
    ];

    this.sendNotification(session, 'mining.notify', params);
  }

  /**
   * Calculate all potential version candidates for AsicBoost / Version Rolling
   */
  private getVersionCandidates(jobVersionHex: string, versionBitsHex: any, sessionMaskHex?: string): number[] {
    const baseVersion = parseInt(jobVersionHex, 16) >>> 0;
    const mask = sessionMaskHex ? parseInt(sessionMaskHex, 16) >>> 0 : 0x1fffe000;
    const candidates = new Set<number>([baseVersion]);

    if (versionBitsHex === undefined || versionBitsHex === null || versionBitsHex === '') {
      return Array.from(candidates);
    }

    let rawBitsNum: number | null = null;
    if (typeof versionBitsHex === 'number') {
      rawBitsNum = versionBitsHex >>> 0;
    } else if (typeof versionBitsHex === 'string') {
      const parsed = parseInt(versionBitsHex.trim(), 16);
      if (!isNaN(parsed)) rawBitsNum = parsed >>> 0;
    }

    if (rawBitsNum !== null) {
      // 1. Direct mask apply
      candidates.add(((baseVersion & ~mask) | (rawBitsNum & mask)) >>> 0);
      candidates.add(((baseVersion & ~mask) | rawBitsNum) >>> 0);
      candidates.add((baseVersion | rawBitsNum) >>> 0);
      candidates.add((baseVersion ^ rawBitsNum) >>> 0);
      candidates.add((baseVersion + rawBitsNum) >>> 0);

      // 2. 32-bit LE/BE byte swap of rawBitsNum
      const buf32 = Buffer.alloc(4);
      buf32.writeUInt32BE(rawBitsNum, 0);
      const swapped32 = buf32.readUInt32LE(0);
      candidates.add(((baseVersion & ~mask) | (swapped32 & mask)) >>> 0);
      candidates.add(((baseVersion & ~mask) | swapped32) >>> 0);
      candidates.add((baseVersion | swapped32) >>> 0);
      candidates.add((baseVersion ^ swapped32) >>> 0);

      // 3. 16-bit byte swap of rawBitsNum
      const buf16 = Buffer.alloc(4);
      buf16.writeUInt32LE(rawBitsNum, 0);
      const swapped16 = buf16.readUInt32BE(0);
      candidates.add(((baseVersion & ~mask) | (swapped16 & mask)) >>> 0);

      // 4. Shifted left by 13 (if rawBitsNum is unshifted bit field)
      const shifted13 = (rawBitsNum << 13) >>> 0;
      candidates.add(((baseVersion & ~mask) | (shifted13 & mask)) >>> 0);

      // 5. Shifted right by 13
      const shiftedRight13 = (rawBitsNum >>> 13) >>> 0;
      candidates.add(((baseVersion & ~mask) | (shiftedRight13 & mask)) >>> 0);

      // 6. Raw version bits passed as full version
      candidates.add(rawBitsNum);
      candidates.add(swapped32);
    }

    return Array.from(candidates);
  }

  /**
   * Generate potential extranonce2 representations for non-standard ASIC miners (Avalon Nano / Canaan)
   */
  private getExt2Candidates(extranonce2Hex: string, extranonce2Size: number): string[] {
    const candidates = new Set<string>([extranonce2Hex]);

    const targetLen = extranonce2Size * 2;
    candidates.add(extranonce2Hex.padStart(targetLen, '0'));
    candidates.add(extranonce2Hex.padEnd(targetLen, '0'));

    try {
      const buf = Buffer.from(extranonce2Hex, 'hex');
      candidates.add(reverseBuffer(buf).toString('hex'));
    } catch (e) {}

    if (extranonce2Hex.length === 8) {
      // 4 bytes: word swap (first 2 bytes <-> last 2 bytes)
      const wordSwapped = extranonce2Hex.substring(4) + extranonce2Hex.substring(0, 4);
      candidates.add(wordSwapped);

      const first2 = extranonce2Hex.substring(0, 4);
      const last2 = extranonce2Hex.substring(4, 8);

      // Avalon / Canaan specific patterns (e.g. c4ad0000)
      candidates.add('0000' + first2);
      candidates.add(first2 + '0000');
      candidates.add('0000' + last2);
      candidates.add(last2 + '0000');

      try {
        const first2Buf = Buffer.from(first2, 'hex');
        const first2Rev = reverseBuffer(first2Buf).toString('hex');
        candidates.add('0000' + first2Rev);
        candidates.add(first2Rev + '0000');

        const last2Buf = Buffer.from(last2, 'hex');
        const last2Rev = reverseBuffer(last2Buf).toString('hex');
        candidates.add('0000' + last2Rev);
        candidates.add(last2Rev + '0000');
      } catch (e) {}
    }

    return Array.from(candidates);
  }

  /**
   * Generate potential nTime candidates
   */
  private getNTimeCandidates(nTimeHex: string, jobNTimeHex: string): string[] {
    const candidates = new Set<string>([nTimeHex, jobNTimeHex]);

    try {
      candidates.add(reverseBuffer(Buffer.from(nTimeHex, 'hex')).toString('hex'));
      candidates.add(reverseBuffer(Buffer.from(jobNTimeHex, 'hex')).toString('hex'));
    } catch (e) {}

    const nTimeInt = parseInt(nTimeHex, 16);
    if (!isNaN(nTimeInt)) {
      for (let offset = -5; offset <= 5; offset++) {
        if (offset === 0) continue;
        const val = (nTimeInt + offset) >>> 0;
        candidates.add(val.toString(16).padStart(8, '0'));
      }
    }

    const jobNTimeInt = parseInt(jobNTimeHex, 16);
    if (!isNaN(jobNTimeInt)) {
      for (let offset = -5; offset <= 5; offset++) {
        if (offset === 0) continue;
        const val = (jobNTimeInt + offset) >>> 0;
        candidates.add(val.toString(16).padStart(8, '0'));
      }
    }

    return Array.from(candidates);
  }

  /**
   * Generate potential Nonce candidates
   */
  private getNonceCandidates(nonceHex: string): string[] {
    const padded = nonceHex.padStart(8, '0');
    const candidates = new Set<string>([padded, nonceHex]);

    try {
      const buf = Buffer.from(padded, 'hex');
      candidates.add(reverseBuffer(buf).toString('hex'));
    } catch (e) {}

    return Array.from(candidates);
  }

  public getActiveSessions(): StratumSession[] {
    return Array.from(this.sessions.values());
  }
}
