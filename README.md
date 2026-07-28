# ntpool

High-performance SHA-256 solo mining pool written in Go, with Stratum V1, Overt AsicBoost support, a LAN-only realtime web dashboard, and optional `ntfy` notifications. Also includes a `zpool-proxy` mode for mining on zpool.ca.

## Overview

`ntpool` เป็นโปรเจกต์ Go ที่รองรับ 2 โหมดหลัก:

1. **`ntpool`** (ค่าเริ่มต้น): solo mining pool แบบพึ่งพา node ของตัวเอง ใช้ Bitcoin RPC สำหรับ block template, ใช้ ZMQ สำหรับรับ block notification แบบทันที, และมี web dashboard สำหรับดู worker, hashrate, difficulty, best share, และ blocks found
2. **`zpool-proxy`**: Stratum TCP proxy + localhost dashboard สำหรับ mine บน zpool.ca โดย proxy จะ inject wallet address และ password (`c=BTC` ฯลฯ) ให้อัตโนมัติ เหมาะกับ miner ที่ใส่ comma ใน password ไม่ได้ เช่น Avalon Nano3s

### ฟีเจอร์ ntpool mode

- Stratum V1 server สำหรับเครื่องขุด SHA-256
- รองรับ Overt AsicBoost / version rolling
- ใช้ block template จาก RPC และ refresh งานผ่าน ZMQ พร้อม backup poller
- Web dashboard แบบ realtime ผ่าน WebSocket
- บันทึก blocks found ลง `data/found_blocks.json`
- แจ้งเตือนผ่าน `ntfy` เมื่อ `submitblock` สำเร็จ
- รีเซ็ต `Best Share` ของทุก session เป็น `0` หลังขุดพบบล็อกสำเร็จ

### ฟีเจอร์ zpool-proxy mode

- Stratum TCP proxy รับ miner แล้วส่งต่อไป zpool.ca upstream
- Inject username (wallet) และ password (`c=BTC` หรือ custom) ใน `mining.authorize` อัตโนมัติ
- Worker suffix หลัง `.` ถูก preserve ไว้ zpool เห็นแต่ละ miner แยกกันพร้อม diff ของตัวเอง
- Localhost-only web dashboard ดึง API จาก zpool.ca ผ่าน proxy
- แจ้งเตือนผ่าน `ntfy` เมื่อ `totalpaid` เพิ่มขึ้น (payout ใหม่)

## Project Structure

```text
ntpool/
├── bitcoin/
│   ├── rpc.go               # Bitcoin RPC client
│   └── zmq.go               # ZMQ subscriber for new blocks
├── config/
│   └── config.go            # Environment-based configuration loader
├── crypto/
│   └── sha256.go            # SHA-256 and block header helpers
├── pool/
│   ├── coinbase.go          # Coinbase transaction builder
│   └── job.go               # Job manager and template conversion
├── public/
│   ├── app.js               # ntpool dashboard frontend
│   ├── index.html           # ntpool dashboard UI
│   ├── style.css            # ntpool dashboard styles
│   └── zpool/
│       ├── app.js           # zpool-proxy dashboard frontend
│       ├── index.html       # zpool-proxy dashboard UI
│       └── style.css        # zpool-proxy dashboard styles
├── stratum/
│   ├── server.go            # Stratum server and share validation flow
│   └── session.go           # Session stats, vardiff, best share tracking
├── web/
│   ├── server.go            # HTTP/WebSocket dashboard server (ntpool mode)
│   ├── zpool_proxy.go       # API proxy + payout monitor (zpool-proxy mode)
│   └── zpool_stratum_proxy.go # Stratum TCP proxy (zpool-proxy mode)
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── main.go                  # Application entry point
└── README.md
```

## Requirements

- Go 1.22+
- **ntpool mode**: Bitcoin-compatible SHA-256 node with RPC enabled, ZMQ `rawblock` publisher optional
- **zpool-proxy mode**: ไม่ต้องการ node ใดๆ ต่อ internet ตรงได้เลย
- Docker / Docker Compose optional

## Configuration

คัดลอก `.env.example` เป็น `.env` แล้วแก้ค่าตาม environment ของคุณ

```bash
cp .env.example .env
```

### ตัวแปรทั้งหมด

| Variable | Description | Default |
| :--- | :--- | :--- |
| `APP_MODE` | โหมดการทำงาน (`ntpool` หรือ `zpool-proxy`) | `ntpool` |
| `STRATUM_PORT` | พอร์ต Stratum (ทั้ง 2 โหมด) | `3333` |
| `WEB_PORT` | พอร์ต web dashboard (ทั้ง 2 โหมด) | `8080` |
| `DEFAULT_DIFF` | difficulty เริ่มต้น (ntpool) | `1024` |
| `ENABLE_VARDIFF` | เปิด vardiff (ntpool) | `false` |
| `MIN_DIFF` | difficulty ต่ำสุดของ vardiff (ntpool) | `64` |
| `MAX_DIFF` | difficulty สูงสุดของ vardiff (ntpool) | `1048576` |
| `VARDIFF_TARGET_SHARES` | จำนวน share เป้าหมายต่อรอบ (ntpool) | `12` |
| `RPC_HOST` | host ของ Bitcoin RPC (ntpool) | `127.0.0.1` |
| `RPC_PORT` | พอร์ต Bitcoin RPC (ntpool) | `8332` |
| `RPC_USER` | RPC username (ntpool) | `bitcoinrpc` |
| `RPC_PASSWORD` | RPC password (ntpool) | `rpcpassword` |
| `RPC_NETWORK` | network name เช่น `mainnet`, `testnet` (ntpool) | `mainnet` |
| `RPC_ALGO` | อัลกอริทึมของ chain (ntpool) | `sha256d` |
| `ZMQ_HOST` | host ของ ZMQ publisher (ntpool) | `127.0.0.1` |
| `ZMQ_PORT` | พอร์ต ZMQ `rawblock` (ntpool) | `28332` |
| `POOL_NAME` | ชื่อ pool (ntpool) | `ntpool SHA-256 Solo Pool` |
| `COIN_SYMBOL` | symbol เหรียญ (ntpool) | `BTC` |
| `COINBASE_TEXT` | ข้อความใน coinbase (ntpool) | `/ntpool/` |
| `POOL_FEE_PERCENT` | ค่า fee (ntpool) | `0.0` |
| `POOL_FEE_ADDRESS` | address รับ fee (ntpool) | `""` |
| `WALLET_ADDRESS` | address รับ coinbase payout (ntpool) | `AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr` |
| `NTFY_SERVER` | URL ของ ntfy server | `http://192.168.1.250:18080` |
| `NTFY_TOPIC` | topic ปลายทางบน ntfy | `ntpool-blocks` |
| `NTFY_USER` | username Basic Auth ของ ntfy | `user` |
| `NTFY_PASSWORD` | password Basic Auth ของ ntfy | `pass` |
| `ZPOOL_API_BASE_URL` | upstream base URL ของ zpool API (zpool-proxy) | `https://www.zpool.ca/api` |
| `ZPOOL_API_USERNAME` | username Basic Auth ของ zpool API (zpool-proxy, optional) | `""` |
| `ZPOOL_API_PASSWORD` | password Basic Auth ของ zpool API (zpool-proxy, optional) | `""` |
| `ZPOOL_WALLET_ADDRESS` | wallet address สำหรับ dashboard (zpool-proxy) | `""` |
| `ZPOOL_NOTIFY_PAYOUT` | แจ้ง ntfy เมื่อ payout ใหม่ (zpool-proxy) | `true` |
| `ZPOOL_POLL_SECONDS` | รอบ polling wallet API วินาที (zpool-proxy) | `60` |
| `ZPOOL_STRATUM_HOST` | upstream stratum host ของ zpool (zpool-proxy) | `sha256.mine.zpool.ca` |
| `ZPOOL_STRATUM_PORT` | upstream stratum port ของ zpool (zpool-proxy) | `3256` |
| `ZPOOL_STRATUM_USERNAME` | wallet inject ใน mining.authorize (zpool-proxy) | `""` |
| `ZPOOL_STRATUM_PASSWORD` | password inject ใน mining.authorize (zpool-proxy) | `c=BTC` |
| `DASHBOARD_USERNAME` | username Basic Auth หน้า dashboard localhost (zpool-proxy, optional) | `""` |
| `DASHBOARD_PASSWORD` | password Basic Auth หน้า dashboard localhost (zpool-proxy, optional) | `""` |

## ntpool Mode

### Connecting Miners

- URL: `stratum+tcp://<SERVER_IP>:3333`
- Username: `<WALLET_ADDRESS>.<WORKER_NAME>` หรือ `<ANY_PREFIX>.<WORKER_NAME>`
- Password: `x`

หมายเหตุ: ระบบใช้ `WALLET_ADDRESS` จาก config เป็นปลายทาง payout เสมอ prefix หน้า `.` ถูกละทิ้ง ใช้เฉพาะชื่อหลังจุดเป็น worker name

### Running Locally

```bash
go run .
```

หรือ build binary ก่อน:

```bash
go build -o ntpool .
./ntpool
```

บน Windows binary ที่ build จะเป็น `ntpool.exe`

### Docker

```bash
docker compose up -d --build
docker compose logs -f ntpool
docker compose down
```

ถ้า Bitcoin node อยู่บน host machine อาจต้องเปิด `extra_hosts` ใน [docker-compose.yml](docker-compose.yml)

### Runtime Notes

- Stratum server ฟังที่ `0.0.0.0:STRATUM_PORT`
- Web dashboard ฟังที่ `0.0.0.0:WEB_PORT` จำกัดเฉพาะ loopback / private LAN
- Dashboard ใช้ WebSocket เพื่อ push stats แบบ realtime
- เมื่อ `submitblock` สำเร็จ ระบบจะส่ง `ntfy` notification และ reset best share ทุก session
- รายการ blocks found เก็บไว้ใน `data/found_blocks.json`

---

## zpool-proxy Mode

### Setup

ตั้งค่าใน `.env`:

```env
APP_MODE=zpool-proxy
WEB_PORT=8080
STRATUM_PORT=3333

ZPOOL_STRATUM_HOST=sha256.mine.zpool.ca
ZPOOL_STRATUM_PORT=3256
ZPOOL_STRATUM_USERNAME=your_wallet_address
ZPOOL_STRATUM_PASSWORD=c=BTC

ZPOOL_WALLET_ADDRESS=your_wallet_address
ZPOOL_NOTIFY_PAYOUT=true
ZPOOL_POLL_SECONDS=60

NTFY_SERVER=http://your-ntfy-server
NTFY_TOPIC=zpool-payouts
NTFY_USER=user
NTFY_PASSWORD=pass

DASHBOARD_USERNAME=admin
DASHBOARD_PASSWORD=strong-password
```

รันแอป:

```bash
go run .
```

### Connecting Miners (zpool-proxy mode)

- URL: `stratum+tcp://<เครื่องนี้>:3333`
- Username: `อะไรก็ได้.<WORKER_NAME>` เช่น `x.nano1`
- Password: `x`

Proxy จะ inject `ZPOOL_STRATUM_USERNAME` แทน wallet และ `ZPOOL_STRATUM_PASSWORD` แทน password ก่อนส่งต่อ zpool โดย worker suffix หลัง `.` ยังคงไว้เพื่อให้ zpool แยก worker

ตัวอย่าง miner ส่ง `x.nano1` / `x` → zpool รับ `your_wallet.nano1` / `c=BTC`

### Dashboard

เปิดที่ `http://localhost:8080` (เข้าได้จาก localhost เท่านั้น)

API endpoints ที่ใช้งานได้:

- `GET /api/zpool/status`
- `GET /api/zpool/currencies`
- `GET /api/zpool/wallet` (ใช้ `ZPOOL_WALLET_ADDRESS` หรือส่ง `?address=...`)

### Payout Notifications

เมื่อเปิด `ZPOOL_NOTIFY_PAYOUT=true` ระบบจะ poll `/wallet` ตามรอบ `ZPOOL_POLL_SECONDS` และส่ง `ntfy` เมื่อ `totalpaid` เพิ่มขึ้น รอบแรกที่เริ่มโปรแกรมจะตั้ง baseline ก่อนโดยไม่แจ้ง

ข้อความที่ได้รับ:
```
ZPOOL PAYOUT DETECTED
Address <wallet>
New payout <delta>
```

---

## Validation

```bash
go build ./...
```

## License

MIT

