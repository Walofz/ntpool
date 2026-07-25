import net from 'net';
import { config } from '../config';

export interface MinerStats {
  address: string;
  workerName: string;
  difficulty: number;
  acceptedShares: number;
  rejectedShares: number;
  bestShareDiff: number;
  hashrate1m: number;
  hashrate5m: number;
  connectedAt: Date;
  lastShareTime: Date | null;
}

export class StratumSession {
  public id: string;
  public socket: net.Socket;
  public ip: string;
  public extranonce1: string; // 4-byte hex
  public extranonce2Size = 4;
  public isSubscribed = false;
  public isAuthorized = false;
  public minerAddress = '';
  public workerName = '';

  // AsicBoost / Version Rolling
  public versionRollingEnabled = false;
  public versionRollingMask = '1fffe000';
  public versionRollingMinBitCount = 16;

  // Vardiff & Difficulty
  public currentDiff: number = config.defaultDiff;
  public shareHistory: Array<{ timestamp: number; diff: number }> = [];

  // Stats
  public acceptedShares = 0;
  public rejectedShares = 0;
  public bestShareDiff = 0;
  public connectedAt: Date = new Date();
  public lastShareTime: Date | null = null;

  constructor(id: string, socket: net.Socket, extranonce1: string) {
    this.id = id;
    this.socket = socket;
    this.ip = socket.remoteAddress || '127.0.0.1';
    this.extranonce1 = extranonce1;
  }

  private lastVardiffTime = Date.now();

  /**
   * Reset Best Share difficulty to 0 (e.g. when a block is found)
   */
  public resetBestShare(): void {
    this.bestShareDiff = 0;
  }

  /**
   * Record share for Vardiff calculation and hashrate tracking
   */
  public recordShare(targetDiff: number, shareDiff?: number): number | null {
    const now = Date.now();
    this.acceptedShares++;
    this.lastShareTime = new Date();

    const best = shareDiff || targetDiff;
    if (best > this.bestShareDiff) {
      this.bestShareDiff = best;
    }

    // Store assigned target diff for hashrate calculation
    this.shareHistory.push({ timestamp: now, diff: targetDiff });

    // Keep last 10 minutes of shares
    const tenMinAgo = now - 600000;
    this.shareHistory = this.shareHistory.filter((s) => s.timestamp >= tenMinAgo);

    // Evaluate Vardiff at most once every 45 seconds (CKPool standard retarget interval)
    if (now - this.lastVardiffTime >= 45000 && this.shareHistory.length >= 10) {
      return this.calculateVardiff(now);
    }

    return null;
  }

  /**
   * Compute new difficulty based on share submission rate (target ~12 shares / min)
   */
  private calculateVardiff(now: number): number | null {
    if (!config.enableVardiff) return null;

    const oldest = this.shareHistory[0];
    const newest = this.shareHistory[this.shareHistory.length - 1];
    const timeDeltaSec = (newest.timestamp - oldest.timestamp) / 1000;

    if (timeDeltaSec < 15) return null;

    const sharesPerMin = (this.shareHistory.length / timeDeltaSec) * 60;
    const targetRate = config.vardiffTargetShares || 12; // 12 shares/min

    let ratio = sharesPerMin / targetRate;

    // Fast initial retarget if submission rate is way off (< 0.4x or > 2.5x)
    if (ratio < 0.4 || ratio > 2.5) {
      ratio = Math.max(0.1, Math.min(10.0, ratio));
    } else if (ratio < 0.7 || ratio > 1.4) {
      ratio = Math.max(0.5, Math.min(2.0, ratio));
    } else {
      return null;
    }

    let newDiff = this.currentDiff * ratio;

    if (newDiff < 1) {
      newDiff = parseFloat(newDiff.toFixed(4));
    } else if (newDiff < 64) {
      newDiff = Math.round(newDiff);
    } else {
      newDiff = Math.round(newDiff / 16) * 16;
    }

    newDiff = Math.max(config.minDiff, Math.min(config.maxDiff, newDiff));

    if (Math.abs(newDiff - this.currentDiff) >= 0.0001 && newDiff !== this.currentDiff) {
      this.currentDiff = newDiff;
      this.lastVardiffTime = now;
      return newDiff;
    }

    return null;
  }

  /**
   * Calculate miner estimated Hashrate (H/s)
   */
  public getHashrate(windowMs: number): number {
    const now = Date.now();
    const cutoff = now - windowMs;
    const recent = this.shareHistory.filter((s) => s.timestamp >= cutoff);
    if (recent.length === 0) return 0;

    let totalDiff = 0;
    for (const s of recent) totalDiff += s.diff;

    // Elapsed duration within window
    const oldestTimestamp = recent[0].timestamp;
    const elapsedSec = Math.max(1, (now - oldestTimestamp) / 1000);
    const durationSec = Math.min(windowMs / 1000, elapsedSec);

    // 1 diff share = 2^32 hashes (4,294,967,296)
    const totalHashes = totalDiff * Math.pow(2, 32);
    return totalHashes / Math.max(10, durationSec);
  }

  public getStats(): MinerStats {
    return {
      address: this.minerAddress || 'unknown',
      workerName: this.workerName || 'worker',
      difficulty: this.currentDiff,
      acceptedShares: this.acceptedShares,
      rejectedShares: this.rejectedShares,
      bestShareDiff: this.bestShareDiff,
      hashrate1m: this.getHashrate(60000),
      hashrate5m: this.getHashrate(300000),
      connectedAt: this.connectedAt,
      lastShareTime: this.lastShareTime,
    };
  }
}
