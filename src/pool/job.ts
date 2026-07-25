import crypto from 'crypto';
import { BlockTemplate } from '../bitcoin/rpc';
import { buildCoinbaseTransaction } from './coinbase';
import { sha256d, reverseBuffer } from '../crypto/sha256';
import { config } from '../config';

export interface MiningJob {
  jobId: string;
  blockHeight: number;
  coinbaseValue: number;
  prevHashStratum: string;
  prevHashRaw: string;
  versionHex: string;
  versionMaskHex: string;
  nBitsHex: string;
  nTimeHex: string;
  merkleBranchHex: string[];
  txsData: string[];
  defaultWitnessCommitment?: string;
  targetHex?: string;
  coinb1: string;
  coinb2: string;
  createdTime: number;
}

export class JobManager {
  private currentJob: MiningJob | null = null;
  private jobMap: Map<string, MiningJob> = new Map();
  private jobCounter = 0;

  /**
   * Format prevhash into standard Stratum 8x32-bit word Little-Endian format
   */
  private formatPrevHashForStratum(prevHashHex: string): string {
    const b = Buffer.from(prevHashHex, 'hex');
    const bLE = reverseBuffer(b);
    for (let i = 0; i < bLE.length; i += 4) {
      const t0 = bLE[i];
      const t1 = bLE[i + 1];
      bLE[i] = bLE[i + 3];
      bLE[i + 1] = bLE[i + 2];
      bLE[i + 2] = t1;
      bLE[i + 3] = t0;
    }
    return bLE.toString('hex');
  }

  /**
   * Calculate Merkle Tree Branch hashes for Stratum (for Coinbase at index 0)
   */
  private calculateMerkleBranch(txs: any[]): string[] {
    if (!txs || txs.length === 0) return [];

    let hashes: Buffer[] = txs.map((tx) => {
      const hashStr = typeof tx === 'string' ? tx : (tx.hash || tx.txid);
      if (hashStr) {
        return reverseBuffer(Buffer.from(hashStr, 'hex'));
      }
      const rawTx = Buffer.from(tx.data, 'hex');
      return sha256d(rawTx);
    });

    const branch: string[] = [];

    while (hashes.length > 0) {
      branch.push(hashes[0].toString('hex'));

      if (hashes.length === 1) break;

      const nextLevel: Buffer[] = [];
      for (let i = 1; i < hashes.length; i += 2) {
        const left = hashes[i];
        const right = i + 1 < hashes.length ? hashes[i + 1] : hashes[i];
        nextLevel.push(sha256d(Buffer.concat([left, right])));
      }
      hashes = nextLevel;
    }

    return branch;
  }

  /**
   * Calculate allowed AsicBoost version mask from block template vbavailable
   */
  private calculateVersionMask(template: BlockTemplate): string {
    // Default overt AsicBoost version-rolling mask: 0x1fffe000 (bits 13 to 28)
    let maskNum = 0x1fffe000;
    if (template.vbavailable) {
      // Respect bits offered by node
      let availableBits = 0;
      for (const key of Object.keys(template.vbavailable)) {
        const bit = template.vbavailable[key];
        availableBits |= 1 << bit;
      }
      if (availableBits > 0) {
        maskNum = availableBits | 0x1fffe000;
      }
    }
    return maskNum.toString(16).padStart(8, '0');
  }

  /**
   * Create new job from Bitcoin RPC BlockTemplate
   */
  public createJob(template: BlockTemplate): MiningJob {
    this.jobCounter++;
    const jobId = this.jobCounter.toString(16).padStart(4, '0');

    const prevHashStratum = this.formatPrevHashForStratum(template.previousblockhash);
    const merkleBranchHex = this.calculateMerkleBranch(template.transactions);
    const versionHex = template.version.toString(16).padStart(8, '0');
    const versionMaskHex = this.calculateVersionMask(template);
    const nBitsHex = template.bits;
    const nTimeHex = template.curtime.toString(16).padStart(8, '0');

    const coinbase = buildCoinbaseTransaction({
      blockHeight: template.height,
      coinbaseValue: template.coinbasevalue,
      minerAddress: config.walletAddress || config.poolFeeAddress || 'AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr',
      extranonce1Size: 4,
      extranonce2Size: 4,
      defaultWitnessCommitment: template.default_witness_commitment,
    });

    const job: MiningJob = {
      jobId,
      blockHeight: template.height,
      coinbaseValue: template.coinbasevalue,
      prevHashStratum,
      prevHashRaw: template.previousblockhash,
      versionHex,
      versionMaskHex,
      nBitsHex,
      nTimeHex,
      merkleBranchHex,
      txsData: template.transactions.map((t) => t.data),
      defaultWitnessCommitment: template.default_witness_commitment,
      targetHex: template.target,
      coinb1: coinbase.coinb1,
      coinb2: coinbase.coinb2,
      createdTime: Date.now(),
    };

    this.currentJob = job;
    this.jobMap.set(jobId, job);

    // Keep map of recent jobs (max 200 jobs for seamless job transitions)
    if (this.jobMap.size > 200) {
      const oldestKey = this.jobMap.keys().next().value;
      if (oldestKey) this.jobMap.delete(oldestKey);
    }

    return job;
  }

  public getCurrentJob(): MiningJob | null {
    return this.currentJob;
  }

  public getJob(jobId: string): MiningJob | undefined {
    return this.jobMap.get(jobId);
  }
}
