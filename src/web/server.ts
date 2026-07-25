import express from 'express';
import http from 'http';
import path from 'path';
import { WebSocketServer, WebSocket } from 'ws';
import { config } from '../config';
import { StratumServer } from '../stratum/server';
import { JobManager } from '../pool/job';
import { nbitsToDifficulty } from '../crypto/sha256';

export class WebDashboardServer {
  private app: express.Application;
  private server: http.Server;
  private wss: WebSocketServer;
  private stratumServer: StratumServer;
  private jobManager: JobManager;

  constructor(stratumServer: StratumServer, jobManager: JobManager) {
    this.app = express();
    this.server = http.createServer(this.app);
    this.wss = new WebSocketServer({ server: this.server });
    this.stratumServer = stratumServer;
    this.jobManager = jobManager;

    this.setupRoutes();
    this.setupWebSockets();
  }

  private setupRoutes(): void {
    this.app.use(express.json());
    this.app.use(express.static(path.join(__dirname, '../../public')));

    // Get Overall Pool Stats
    this.app.get('/api/stats', (req, res) => {
      const stats = this.calculatePoolStats();
      res.json(stats);
    });

    // Get Stats for Specific Miner Address
    this.app.get('/api/miner/:address', (req, res) => {
      const addr = req.params.address.trim();
      const sessions = this.stratumServer.getActiveSessions().filter((s) => s.minerAddress === addr);

      if (sessions.length === 0) {
        return res.status(404).json({ error: 'Miner not connected' });
      }

      let totalHashrate1m = 0;
      let totalHashrate5m = 0;
      let totalAccepted = 0;
      let totalRejected = 0;
      let maxBestDiff = 0;

      const workers = sessions.map((s) => {
        const stats = s.getStats();
        totalHashrate1m += stats.hashrate1m;
        totalHashrate5m += stats.hashrate5m;
        totalAccepted += stats.acceptedShares;
        totalRejected += stats.rejectedShares;
        if (stats.bestShareDiff > maxBestDiff) maxBestDiff = stats.bestShareDiff;

        return {
          workerName: stats.workerName,
          ip: s.ip,
          difficulty: stats.difficulty,
          hashrate1m: stats.hashrate1m,
          hashrate5m: stats.hashrate5m,
          acceptedShares: stats.acceptedShares,
          rejectedShares: stats.rejectedShares,
          bestShareDiff: stats.bestShareDiff,
          asicboost: s.versionRollingEnabled,
          connectedAt: stats.connectedAt,
        };
      });

      res.json({
        address: addr,
        workerCount: workers.length,
        hashrate1m: totalHashrate1m,
        hashrate5m: totalHashrate5m,
        totalAccepted,
        totalRejected,
        bestShareDiff: maxBestDiff,
        workers,
      });
    });
  }

  private calculatePoolStats() {
    const sessions = this.stratumServer.getActiveSessions();
    const currentJob = this.jobManager.getCurrentJob();

    const uniqueMiners = new Set(sessions.map((s) => s.minerAddress));
    let poolHashrate1m = 0;
    let poolHashrate5m = 0;
    let totalAcceptedShares = 0;
    let totalRejectedShares = 0;

    const workersList = sessions.map((s) => {
      const st = s.getStats();
      poolHashrate1m += st.hashrate1m;
      poolHashrate5m += st.hashrate5m;
      totalAcceptedShares += st.acceptedShares;
      totalRejectedShares += st.rejectedShares;

      return {
        address: st.address,
        workerName: st.workerName,
        difficulty: st.difficulty,
        hashrate1m: st.hashrate1m,
        hashrate5m: st.hashrate5m,
        asicboost: s.versionRollingEnabled,
        bestShareDiff: st.bestShareDiff,
      };
    });

    return {
      poolName: config.poolName,
      stratumPort: config.stratumPort,
      network: config.network,
      coinSymbol: config.coinSymbol,
      blockHeight: currentJob ? currentJob.blockHeight : 0,
      networkDifficulty: currentJob ? nbitsToDifficulty(currentJob.nBitsHex) : 0,
      activeMiners: uniqueMiners.size,
      connectedWorkers: sessions.length,
      poolHashrate1m,
      poolHashrate5m,
      totalAcceptedShares,
      totalRejectedShares,
      blocksFound: this.stratumServer.foundBlocks,
      workers: workersList,
    };
  }

  private setupWebSockets(): void {
    this.wss.on('connection', (ws) => {
      // Send initial stats
      ws.send(JSON.stringify(this.calculatePoolStats()));
    });

    // Broadcast stats every 2 seconds
    setInterval(() => {
      const stats = JSON.stringify(this.calculatePoolStats());
      for (const client of this.wss.clients) {
        if (client.readyState === WebSocket.OPEN) {
          client.send(stats);
        }
      }
    }, 2000);
  }

  public start(): void {
    this.server.listen(config.webPort, () => {
      console.log(`[Web Dashboard] Server running at http://localhost:${config.webPort}`);
    });
  }
}
