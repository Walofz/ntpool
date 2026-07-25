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
    const algo = process.env.RPC_ALGO || 'sha256d';

    // 1. Try rules: ['segwit'] + algo: sha256d
    try {
      return await this.call<BlockTemplate>('getblocktemplate', [{ rules: ['segwit'], algo }]);
    } catch (err1: any) {
      // 2. Try ONLY algo: sha256d (without segwit rule)
      try {
        return await this.call<BlockTemplate>('getblocktemplate', [{ algo }]);
      } catch (err2: any) {
        // 3. Try algo: 'sha256'
        try {
          return await this.call<BlockTemplate>('getblocktemplate', [{ algo: 'sha256' }]);
        } catch (err3: any) {
          // 4. Fallback for standard Bitcoin Core (no algo parameter)
          return await this.call<BlockTemplate>('getblocktemplate', [{ rules: ['segwit'] }]);
        }
      }
    }
  }

  public async submitBlock(hexData: string): Promise<string | null> {
    return this.call<string | null>('submitblock', [hexData]);
  }

  public async getBlockchainInfo(): Promise<any> {
    return this.call<any>('getblockchaininfo', []);
  }

  public async getNetworkInfo(): Promise<any> {
    return this.call<any>('getnetworkinfo', []);
  }

  /**
   * Auto-detect coin symbol from RPC Node (getnetworkinfo / getblockchaininfo)
   */
  public async detectCoinSymbol(): Promise<string> {
    // 1. If explicitly set in process.env.COIN_SYMBOL, prefer it
    if (process.env.COIN_SYMBOL && process.env.COIN_SYMBOL.trim() !== '') {
      return process.env.COIN_SYMBOL.trim().toUpperCase();
    }

    // 2. Query node RPC getnetworkinfo for specific altcoin subversions
    try {
      const netInfo: any = await this.getNetworkInfo();
      if (netInfo && netInfo.subversion) {
        const sub = String(netInfo.subversion).toLowerCase();
        if (sub.includes('digibyte')) return 'DGB';
        if (sub.includes('auroracoin')) return 'AUR';
        if (sub.includes('bitcoin cash') || sub.includes('bch')) return 'BCH';
        if (sub.includes('bitcoin sv') || sub.includes('bsv')) return 'BSV';
        if (sub.includes('litecoin')) return 'LTC';
        if (sub.includes('peercoin')) return 'PPC';
        if (sub.includes('namecoin')) return 'NMC';
        if (sub.includes('syscoin')) return 'SYS';
        if (sub.includes('dogecoin')) return 'DOGE';
        if (sub.includes('viacoin')) return 'VIA';
        if (sub.includes('dash')) return 'DASH';
      }
    } catch (err) {}

    // 3. Query node RPC getblockchaininfo
    try {
      const chainInfo: any = await this.getBlockchainInfo();
      if (chainInfo && chainInfo.chain) {
        const chain = String(chainInfo.chain).toLowerCase();
        if (chain.includes('dgb') || chain.includes('digibyte')) return 'DGB';
        if (chain.includes('bch')) return 'BCH';
      }
    } catch (err) {}

    return 'BTC';
  }
}
