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
   * CKPool-style: gentle retarget, max ±25% per 60-second interval
   */
  private calculateVardiff(now: number): number | null {
    if (!config.enableVardiff) return null;

    const oldest = this.shareHistory[0];
    const newest = this.shareHistory[this.shareHistory.length - 1];
    const timeDeltaSec = (newest.timestamp - oldest.timestamp) / 1000;

    if (timeDeltaSec < 30) return null;

    const sharesPerMin = (this.shareHistory.length / timeDeltaSec) * 60;
    const targetRate = config.vardiffTargetShares || 12; // 12 shares/min

    const ratio = sharesPerMin / targetRate;

    // Only adjust if submission rate is clearly off (< 0.5x or > 2x target)
    if (ratio >= 0.5 && ratio <= 2.0) return null;

    // Gentle step: max ±25% per retarget
    const clampedRatio = Math.max(0.75, Math.min(1.25, ratio));

    let newDiff = this.currentDiff * clampedRatio;

    // Round to clean values
    if (newDiff < 64) {
      newDiff = Math.max(1, Math.round(newDiff));
    } else {
      newDiff = Math.round(newDiff / 32) * 32;
    }

    newDiff = Math.max(config.minDiff, Math.min(config.maxDiff, newDiff));

    if (newDiff !== this.currentDiff) {
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
