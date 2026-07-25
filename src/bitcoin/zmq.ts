import http from 'http';
import { BitcoinRpcClient } from './rpc';
import { JobManager } from '../pool/job';
import { StratumServer } from '../stratum/server';
import { config } from '../config';

export class BlockNotifier {
  private rpc: BitcoinRpcClient;
  private jobManager: JobManager;
  private stratumServer: StratumServer;
  private pollInterval: NodeJS.Timeout | null = null;
  private lastBlockHash = '';
  private lastErrorLog = 0;

  constructor(rpc: BitcoinRpcClient, jobManager: JobManager, stratumServer: StratumServer) {
    this.rpc = rpc;
    this.jobManager = jobManager;
    this.stratumServer = stratumServer;
  }

  public async start(): Promise<void> {
    console.log('[Notifier] Starting Bitcoin Node Poller & Block Observer...');
    await this.checkAndUpdateJob();

    // Poll getblocktemplate every 3 seconds for fast job updates
    this.pollInterval = setInterval(() => {
      this.checkAndUpdateJob().catch((err) => {
        // Silent catch for network hiccups
      });
    }, 3000);
  }

  public async checkAndUpdateJob(): Promise<void> {
    try {
      const template = await this.rpc.getBlockTemplate();
      const newPrevHash = template.previousblockhash;

      if (newPrevHash !== this.lastBlockHash) {
        console.log(`[Notifier] New Block Detected on Network! Height #${template.height}, Hash: ${newPrevHash.substring(0, 16)}...`);
        this.lastBlockHash = newPrevHash;
        const job = this.jobManager.createJob(template);
        this.stratumServer.broadcastJob(job, true);
      }
    } catch (err: any) {
      if (Date.now() - this.lastErrorLog > 10000) {
        console.error(`[RPC Error] Failed to fetch block template from Bitcoin Core (${config.rpcHost}:${config.rpcPort}): ${err.message}`);
        this.lastErrorLog = Date.now();
      }
    }
  }

  public stop(): void {
    if (this.pollInterval) {
      clearInterval(this.pollInterval);
    }
  }
}
