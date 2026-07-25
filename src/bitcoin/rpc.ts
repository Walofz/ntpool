import http from 'http';
import { config } from '../config';

export interface BlockTemplateTx {
  data: string;
  txid: string;
  hash: string;
  depends: number[];
  fee: number;
  sigops: number;
  weight?: number;
}

export interface BlockTemplate {
  version: number;
  rules: string[];
  vbavailable: Record<string, number>;
  vbrequired: number;
  previousblockhash: string;
  transactions: BlockTemplateTx[];
  coinbaseaux: Record<string, string>;
  coinbasevalue: number;
  target: string;
  mintime: number;
  mutable: string[];
  noncerange: string;
  sigoplimit: number;
  sizelimit: number;
  curtime: number;
  bits: string;
  height: number;
  default_witness_commitment?: string;
}

export class BitcoinRpcClient {
  private host: string;
  private port: number;
  private user: string;
  private pass: string;

  constructor() {
    this.host = config.rpcHost;
    this.port = config.rpcPort;
    this.user = config.rpcUser;
    this.pass = config.rpcPass;
  }

  public async call<T>(method: string, params: any[] = []): Promise<T> {
    return new Promise((resolve, reject) => {
      const auth = Buffer.from(`${this.user}:${this.pass}`).toString('base64');
      const payload = JSON.stringify({
        jsonrpc: '1.0',
        id: 'ntpool',
        method,
        params,
      });

      const options: http.RequestOptions = {
        hostname: this.host,
        port: this.port,
        path: '/',
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Content-Length': Buffer.byteLength(payload),
          Authorization: `Basic ${auth}`,
        },
        timeout: 10000,
      };

      const req = http.request(options, (res) => {
        let data = '';
        res.on('data', (chunk) => {
          data += chunk;
        });
        res.on('end', () => {
          if (res.statusCode && res.statusCode >= 400 && res.statusCode !== 500) {
            return reject(new Error(`RPC HTTP Error ${res.statusCode}: ${data}`));
          }
          try {
            const parsed = JSON.parse(data);
            if (parsed.error) {
              return reject(new Error(`RPC Error (${parsed.error.code}): ${parsed.error.message}`));
            }
            resolve(parsed.result);
          } catch (e: any) {
            reject(new Error(`Failed to parse RPC response: ${e.message}`));
          }
        });
      });

      req.on('error', (err) => {
        reject(err);
      });

      req.on('timeout', () => {
        req.destroy(new Error('RPC Request Timeout'));
      });

      req.write(payload);
      req.end();
    });
  }

  public async getBlockTemplate(): Promise<BlockTemplate> {
    const params: any = {
      rules: ['segwit'],
      algo: process.env.RPC_ALGO || 'sha256d',
    };

    try {
      return await this.call<BlockTemplate>('getblocktemplate', [params]);
    } catch (err: any) {
      // Fallback if node does not accept 'algo' parameter (e.g. Bitcoin Core)
      delete params.algo;
      return await this.call<BlockTemplate>('getblocktemplate', [params]);
    }
  }

  public async submitBlock(hexData: string): Promise<string | null> {
    return this.call<string | null>('submitblock', [hexData]);
  }

  public async getBlockchainInfo(): Promise<any> {
    return this.call<any>('getblockchaininfo', []);
  }
}
