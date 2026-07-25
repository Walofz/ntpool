import dotenv from 'dotenv';
import path from 'path';

dotenv.config({ path: path.join(__dirname, '../.env') });

export interface PoolConfig {
  stratumPort: number;
  webPort: number;
  defaultDiff: number;
  minDiff: number;
  maxDiff: number;
  vardiffTargetShares: number;
  rpcHost: string;
  rpcPort: number;
  rpcUser: string;
  rpcPass: string;
  network: string;
  zmqHost: string;
  zmqPort: number;
  poolName: string;
  coinbaseText: string;
  poolFeePercent: number;
  poolFeeAddress: string;
  enableVardiff: boolean;
}

export const config: PoolConfig = {
  stratumPort: parseInt(process.env.STRATUM_PORT || '3333', 10),
  webPort: parseInt(process.env.WEB_PORT || '8080', 10),
  defaultDiff: parseFloat(process.env.DEFAULT_DIFF || '1024'),
  minDiff: parseFloat(process.env.MIN_DIFF || '64'),
  maxDiff: parseFloat(process.env.MAX_DIFF || '1048576'),
  vardiffTargetShares: parseInt(process.env.VARDIFF_TARGET_SHARES || '12', 10),
  rpcHost: process.env.RPC_HOST || '127.0.0.1',
  rpcPort: parseInt(process.env.RPC_PORT || '8332', 10),
  rpcUser: process.env.RPC_USER || 'bitcoinrpc',
  rpcPass: process.env.RPC_PASSWORD || 'rpcpassword',
  network: process.env.RPC_NETWORK || 'regtest',
  zmqHost: process.env.ZMQ_HOST || '127.0.0.1',
  zmqPort: parseInt(process.env.ZMQ_PORT || '28332', 10),
  poolName: process.env.POOL_NAME || 'ntpool SHA-256 Solo Pool',
  coinbaseText: process.env.COINBASE_TEXT || '/ntpool/',
  poolFeePercent: parseFloat(process.env.POOL_FEE_PERCENT || '0.0'),
  poolFeeAddress: process.env.POOL_FEE_ADDRESS || '',
  enableVardiff: process.env.ENABLE_VARDIFF === 'true' || process.env.ENABLE_VARDIFF === '1',
};
