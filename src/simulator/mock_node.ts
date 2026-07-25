import http from 'http';
import crypto from 'crypto';

const PORT = 8332;

let currentHeight = 850000;
let currentPrevHash = crypto.randomBytes(32).toString('hex');

const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', (chunk) => (body += chunk));
  req.on('end', () => {
    try {
      const payload = JSON.parse(body);
      const { method, id } = payload;

      let result: any = null;

      if (method === 'getblocktemplate') {
        result = {
          version: 0x20000000,
          rules: ['segwit', 'csv'],
          vbavailable: {
            versionbits: 29,
          },
          vbrequired: 0,
          previousblockhash: currentPrevHash,
          transactions: [
            {
              data: '01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff0100f2052a010000001976a914111111111111111111111111111111111111111188ac00000000',
              txid: crypto.randomBytes(32).toString('hex'),
              hash: crypto.randomBytes(32).toString('hex'),
              depends: [],
              fee: 50000,
              sigops: 2,
            },
          ],
          coinbaseaux: { flags: '062f47524f512f' },
          coinbasevalue: 312500000 + 50000, // 3.125 BTC + tx fee
          target: '00000000ffff0000000000000000000000000000000000000000000000000000',
          mintime: Math.floor(Date.now() / 1000) - 600,
          mutable: ['time', 'transactions', 'prevblock'],
          noncerange: '00000000ffffffff',
          sigoplimit: 80000,
          sizelimit: 4000000,
          curtime: Math.floor(Date.now() / 1000),
          bits: '1d00ffff', // Low target for regtest testing
          height: currentHeight,
        };
      } else if (method === 'submitblock') {
        result = null; // null indicates block accepted
        console.log(`[Mock Node] ACCEPTED BLOCK #${currentHeight}!`);
        currentHeight++;
        currentPrevHash = crypto.randomBytes(32).toString('hex');
      } else if (method === 'getblockchaininfo') {
        result = {
          chain: 'regtest',
          blocks: currentHeight,
          headers: currentHeight,
          bestblockhash: currentPrevHash,
        };
      }

      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ jsonrpc: '1.0', id, result, error: null }));
    } catch (e: any) {
      res.writeHead(500);
      res.end(JSON.stringify({ error: e.message }));
    }
  });
});

server.listen(PORT, () => {
  console.log(`[Mock Bitcoin Node] Running on http://127.0.0.1:${PORT}`);
});
