# ntpool

High-performance SHA-256 solo mining pool written in Go, with Stratum V1, Overt AsicBoost support, a LAN-only realtime web dashboard, and optional `ntfy` block-found notifications.

## Overview

`ntpool` เป็นโปรเจกต์ Go ที่รองรับ 2 โหมดหลัก:

1. `ntpool` (ค่าเริ่มต้น): solo mining pool แบบพึ่งพา node ของตัวเอง โดยเน้น path การแจกงานและตรวจ share ที่ตรงไปตรงมา ใช้ Bitcoin RPC สำหรับ block template, ใช้ ZMQ สำหรับรับ block notification แบบทันที, และมี web dashboard สำหรับดู worker, hashrate, difficulty, best share, และ blocks found
2. `zpool-proxy`: localhost dashboard + API proxy สำหรับดึงข้อมูลจาก `zpool.ca` ผ่านเครื่องของคุณ

ฟีเจอร์หลักของโปรเจกต์ปัจจุบัน:

- Stratum V1 server สำหรับเครื่องขุด SHA-256
- รองรับ Overt AsicBoost / version rolling
- ใช้ block template จาก RPC และ refresh งานผ่าน ZMQ พร้อม backup poller
- Web dashboard แบบ realtime ผ่าน WebSocket
- บันทึก blocks found ลง `data/found_blocks.json`
- แจ้งเตือนผ่าน `ntfy` เมื่อ `submitblock` สำเร็จ
- รีเซ็ต `Best Share` ของทุก session เป็น `0` หลังขุดพบบล็อกสำเร็จ

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
│   ├── app.js               # Dashboard frontend logic
│   ├── index.html           # Dashboard UI
│   └── style.css            # Dashboard styles
├── stratum/
│   ├── server.go            # Stratum server and share validation flow
│   └── session.go           # Session stats, vardiff, best share tracking
├── web/
│   └── server.go            # HTTP/WebSocket dashboard server
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── main.go                  # Application entry point
└── README.md
```

## Requirements

- Go 1.22+
- Bitcoin-compatible SHA-256 node with RPC enabled
- ZMQ `rawblock` publisher enabled if you want instant new-block updates
- Docker / Docker Compose optional

## Configuration

คัดลอก `.env.example` เป็น `.env` แล้วแก้ค่าตาม environment ของคุณ

ตัวอย่างบน Linux/macOS:

```bash
cp .env.example .env
```

ตัวแปรที่รองรับในโปรเจกต์ปัจจุบัน:

| Variable | Description | Default |
| :--- | :--- | :--- |
| `STRATUM_PORT` | พอร์ตสำหรับ Stratum server | `3333` |
| `WEB_PORT` | พอร์ตสำหรับ web dashboard | `8080` |
| `DEFAULT_DIFF` | difficulty เริ่มต้นสำหรับ worker ใหม่ | `1024` |
| `ENABLE_VARDIFF` | เปิดใช้งาน vardiff หรือไม่ | `false` |
| `MIN_DIFF` | difficulty ต่ำสุดของ vardiff | `64` |
| `MAX_DIFF` | difficulty สูงสุดของ vardiff | `1048576` |
| `VARDIFF_TARGET_SHARES` | จำนวน share เป้าหมายต่อรอบสำหรับ vardiff | `12` |
| `RPC_HOST` | host ของ Bitcoin RPC | `127.0.0.1` |
| `RPC_PORT` | พอร์ต Bitcoin RPC | `8332` |
| `RPC_USER` | RPC username | `bitcoinrpc` |
| `RPC_PASSWORD` | RPC password | `rpcpassword` |
| `RPC_NETWORK` | network name ที่ใช้แสดงผล เช่น `mainnet`, `testnet`, `regtest` | `mainnet` |
| `RPC_ALGO` | อัลกอริทึมของ chain | `sha256d` |
| `ZMQ_HOST` | host ของ ZMQ publisher | `127.0.0.1` |
| `ZMQ_PORT` | พอร์ตของ ZMQ `rawblock` | `28332` |
| `POOL_NAME` | ชื่อ pool | `ntpool SHA-256 Solo Pool` |
| `COIN_SYMBOL` | symbol ของเหรียญที่ใช้แสดงผล | `BTC` |
| `COINBASE_TEXT` | ข้อความที่แทรกใน coinbase transaction | `/ntpool/` |
| `POOL_FEE_PERCENT` | ค่า fee ของ pool | `0.0` |
| `POOL_FEE_ADDRESS` | ปลายทางสำหรับ fee ของ pool | `""` |
| `WALLET_ADDRESS` | address สำหรับรับ coinbase payout | `AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr` |
| `NTFY_SERVER` | URL ของ ntfy server | `http://192.168.1.250:18080` |
| `NTFY_TOPIC` | topic ปลายทางบน ntfy | `ntpool-blocks` |
| `NTFY_USER` | username สำหรับ Basic Auth ของ ntfy | `user` |
| `NTFY_PASSWORD` | password สำหรับ Basic Auth ของ ntfy | `pass` |
| `ZPOOL_API_BASE_URL` | upstream base URL สำหรับ Zpool API (ใช้ในโหมด `zpool-proxy`) | `https://www.zpool.ca/api` |
| `ZPOOL_API_USERNAME` | username สำหรับ upstream Basic Auth ของ Zpool API (optional) | `""` |
| `ZPOOL_API_PASSWORD` | password สำหรับ upstream Basic Auth ของ Zpool API (optional) | `""` |
| `ZPOOL_WALLET_ADDRESS` | wallet address เริ่มต้นสำหรับ `/api/zpool/wallet` (ใช้ในโหมด `zpool-proxy`) | `""` |
| `ZPOOL_NOTIFY_PAYOUT` | เปิด/ปิดการแจ้งเตือน `ntfy` เมื่อ `totalpaid` เพิ่มขึ้นในโหมด `zpool-proxy` | `true` |
| `ZPOOL_POLL_SECONDS` | รอบเวลา polling wallet API เพื่อเช็ก payout ใหม่ในโหมด `zpool-proxy` | `60` |
| `DASHBOARD_USERNAME` | username สำหรับล็อกอินหน้า dashboard บน localhost (optional) | `""` |
| `DASHBOARD_PASSWORD` | password สำหรับล็อกอินหน้า dashboard บน localhost (optional) | `""` |
| `APP_MODE` | โหมดการทำงานของแอป (`ntpool` หรือ `zpool-proxy`) | `ntpool` |

## Zpool Proxy Dashboard Mode

ตั้งค่าตัวอย่างใน `.env`:

```env
APP_MODE=zpool-proxy
WEB_PORT=8080
ZPOOL_API_BASE_URL=https://www.zpool.ca/api
ZPOOL_WALLET_ADDRESS=ใส่กระเป๋าของคุณ
ZPOOL_NOTIFY_PAYOUT=true
ZPOOL_POLL_SECONDS=60
DASHBOARD_USERNAME=admin
DASHBOARD_PASSWORD=strong-password
```

รันแอป:

```bash
go run .
```

เปิดหน้า dashboard:

```text
http://localhost:8080
```

API ที่ใช้งานได้ในโหมดนี้:

- `GET /api/zpool/status`
- `GET /api/zpool/currencies`
- `GET /api/zpool/wallet` (ใช้ `ZPOOL_WALLET_ADDRESS` หรือส่ง `?address=...`)

เมื่อเปิด `ZPOOL_NOTIFY_PAYOUT=true` ระบบจะ poll `/wallet` ตามรอบ `ZPOOL_POLL_SECONDS` และส่ง `ntfy` เมื่อ `totalpaid` เพิ่มขึ้น (baseline รอบแรกจะไม่แจ้ง)

## Running Locally

ติดตั้ง dependency และรันตรงจาก source:

```bash
go run .
```

หรือ build binary ก่อน:

```bash
go build -o ntpool .
./ntpool
```

บน Windows binary ที่ build จะเป็น `ntpool.exe`

## Docker

รันด้วย Docker Compose:

```bash
docker compose up -d --build
```

ดู logs:

```bash
docker compose logs -f ntpool
```

หยุด container:

```bash
docker compose down
```

ถ้า Bitcoin node อยู่บน host machine อาจต้องเปิด `extra_hosts` ใน [docker-compose.yml](docker-compose.yml)

## Runtime Notes

- Stratum server ฟังที่ `0.0.0.0:STRATUM_PORT`
- Web dashboard ฟังที่ `0.0.0.0:WEB_PORT` แต่มี middleware จำกัดการเข้าถึงเฉพาะ loopback / private LAN
- Dashboard ใช้ WebSocket เพื่อ push stats แบบ realtime
- เมื่อ `submitblock` สำเร็จ ระบบจะส่ง `ntfy` notification และ reset best share ของทุก session
- รายการ blocks found ถูกเก็บไว้ใน `data/found_blocks.json`

## Connecting Miners

ตั้งค่า miner ดังนี้:

- URL: `stratum+tcp://<SERVER_IP>:3333`
- Username: `<WALLET_ADDRESS>.<WORKER_NAME>` หรือ `<ANY_PREFIX>.<WORKER_NAME>`
- Password: `x`

หมายเหตุ: โปรเจกต์นี้จะใช้ `WALLET_ADDRESS` จาก config เป็นปลายทาง payout เสมอ
ส่วน prefix หน้า `.` จะถูกละทิ้ง และใช้เฉพาะชื่อหลังจุดเป็น worker name ที่แสดงใน dashboard เท่านั้น

ตัวอย่าง:

```text
anyprefix.s21-01
```

## Validation

คำสั่งที่ใช้เช็กโปรเจกต์หลังแก้โค้ด:

```bash
go build ./...
```

## License

MIT
