# ntpool

High-performance SHA-256 solo mining pool written in Go, with Stratum V1, Overt AsicBoost support, a LAN-only realtime web dashboard, and optional `ntfy` and Discord block-found notifications.

## Overview

`ntpool` เป็น solo mining pool แบบพึ่งพา node ของตัวเอง โดยเน้น path การแจกงานและตรวจ share ที่ตรงไปตรงมา ใช้ Bitcoin RPC สำหรับ block template, ใช้ ZMQ สำหรับรับ block notification แบบทันที, และมี web dashboard สำหรับดู worker, hashrate, difficulty, best share, และ blocks found

ฟีเจอร์หลัก:

- Stratum V1 server สำหรับเครื่องขุด SHA-256
- เลือกเหรียญด้วย `COIN` (`aur` / `dgb` / `btc`) แล้วปรับ parameter ของ `getblocktemplate` ให้ตรงกับ node เอง
- รองรับ Overt AsicBoost / version rolling
- ใช้ block template จาก RPC และ refresh งานผ่าน ZMQ พร้อม backup poller
- Web dashboard แบบ realtime ผ่าน WebSocket
- บันทึก blocks found ลง `data/found_blocks.json`
- แจ้งเตือนผ่าน `ntfy` เมื่อ `submitblock` สำเร็จ
- แจ้งเตือนผ่าน Discord webhook เมื่อ `submitblock` สำเร็จ
- รีเซ็ต `Best Share` ของทุก session เป็น `0` หลังขุดพบบล็อกสำเร็จ

## Project Structure

```text
ntpool/
├── bitcoin/
│   ├── rpc.go               # Bitcoin RPC client
│   └── zmq.go               # ZMQ subscriber for new blocks
├── config/
│   ├── coin.go              # Coin profiles (COIN): gbt params, algo, witness rules
│   └── config.go            # Environment-based configuration loader
├── crypto/
│   └── sha256.go            # SHA-256 and block header helpers
├── pool/
│   ├── address.go           # Payout address to scriptPubKey decoding
│   ├── coinbase.go          # Coinbase transaction builder
│   ├── job.go               # Job manager and template conversion
│   └── witness.go           # BIP141 witness commitment and txid helpers
├── public/
│   ├── app.js               # Dashboard frontend logic
│   ├── index.html           # Dashboard UI
│   └── terminal.css         # Dashboard terminal-console styles
├── stratum/
│   ├── duplicate.go         # Duplicate share detection per session
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
- Bitcoin-derived node with RPC enabled (Bitcoin, Auroracoin หรือ DigiByte — ตั้ง `COIN` ให้ตรงกับเหรียญ)
- pool คำนวณ proof of work แบบ SHA-256d เท่านั้น (`RPC_ALGO=sha256d`); block ของ algo อื่นจะถูก log เตือนและขุดไม่ได้
- ZMQ `rawblock` publisher enabled if you want instant new-block updates
- Docker / Docker Compose optional

## Configuration

คัดลอก `.env.example` เป็น `.env` แล้วแก้ค่าตาม environment ของคุณ

```bash
cp .env.example .env
```

| Variable | Description | Default |
| :--- | :--- | :--- |
| `STRATUM_PORT` | พอร์ตสำหรับ Stratum server | `3333` |
| `WEB_PORT` | พอร์ตสำหรับ web dashboard | `8080` |
| `DEFAULT_DIFF` | difficulty เริ่มต้นสำหรับ worker ใหม่ | `1024` |
| `ENABLE_VARDIFF` | เปิดใช้งาน vardiff หรือไม่ | `false` |
| `MIN_DIFF` | difficulty ต่ำสุดของ vardiff | `64` |
| `MAX_DIFF` | difficulty สูงสุดของ vardiff | `1048576` |
| `VARDIFF_TARGET_SHARES` | จำนวน share เป้าหมายต่อรอบสำหรับ vardiff | `12` |
| `LOG_SHARES` | log ทุก share ที่ถูก accept (บรรทัด `[Share] accept`); share ที่ถูก reject จะ log เสมอ | `false` |
| `RPC_HOST` | host ของ Bitcoin RPC | `127.0.0.1` |
| `RPC_PORT` | พอร์ต Bitcoin RPC | `8332` |
| `RPC_USER` | RPC username | `bitcoinrpc` |
| `RPC_PASSWORD` | RPC password | `rpcpassword` |
| `RPC_NETWORK` | network name ที่ใช้แสดงผล เช่น `mainnet`, `testnet`, `regtest` | `mainnet` |
| `RPC_ALGO` | อัลกอริทึมของ chain (ใช้ทั้ง hash header และส่งให้ node ที่รองรับ multi-algo) | `sha256d` |
| `COIN` | coin profile ที่เลือกใช้: `aur`, `dgb`, `btc` (ว่าง = generic segwit) — กำหนด parameter ของ `getblocktemplate` ให้เอง; ถ้าไม่ตั้งจะเดาจาก `COIN_SYMBOL` | `""` |
| `ZMQ_HOST` | host ของ ZMQ publisher | `127.0.0.1` |
| `ZMQ_PORT` | พอร์ตของ ZMQ `rawblock` | `28332` |
| `POOL_NAME` | ชื่อ pool | `ntpool SHA-256 Solo Pool` |
| `COIN_SYMBOL` | symbol ของเหรียญที่ใช้แสดงผล | `BTC` |
| `MARKET_API_URL` | URL ของ market API ที่คืน JSON รูปแบบ `BUY[0].price`; รองรับ `{coin}`/`{quote}`; refresh ทุก 5 นาที | Freiexchange orderbook template |
| `MARKET_COIN_SYMBOL` | เหรียญฝั่ง market; ค่าเริ่มต้นตาม `COIN_SYMBOL` | `COIN_SYMBOL` |
| `MARKET_QUOTE_SYMBOL` | สกุลเงินที่ใช้แสดงราคา | `BTC` |
| `COINBASE_TEXT` | ข้อความที่แทรกใน coinbase transaction | `/ntpool/` |
| `POOL_FEE_PERCENT` | ค่า fee ของ pool | `0.0` |
| `POOL_FEE_ADDRESS` | ปลายทางสำหรับ fee ของ pool | `""` |
| `WALLET_ADDRESS` | address สำหรับรับ coinbase payout | `AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr` |
| `ENABLE_NTFY` | เปิดการแจ้งเตือนบล็อกผ่าน ntfy หรือไม่ | `false` |
| `NTFY_SERVER` | URL ของ ntfy server | `http://192.168.1.250:18080` |
| `NTFY_TOPIC` | topic ปลายทางบน ntfy | `ntpool-blocks` |
| `NTFY_USER` | username สำหรับ Basic Auth ของ ntfy | `user` |
| `NTFY_PASSWORD` | password สำหรับ Basic Auth ของ ntfy | `pass` |
| `ENABLE_DISCORD` | เปิดการแจ้งเตือนบล็อกผ่าน Discord หรือไม่ | `false` |
| `DISCORD_WEBHOOK_URL` | Discord webhook URL สำหรับแจ้งเตือนเมื่อพบบล็อก | `""` |
| `DISCORD_USERNAME` | ชื่อผู้ส่งที่แสดงใน Discord (ไม่บังคับ) | `""` |
| `DISCORD_AVATAR_URL` | URL รูปภาพสาธารณะสำหรับ avatar ของผู้ส่ง เช่น PNG (ไม่บังคับ) | `""` |

## Coin Profiles

เลือกเหรียญที่จะขุดด้วย `COIN` อย่างเดียว ไม่ต้องแก้โค้ด:

```env
COIN=dgb
```

| profile | getblocktemplate | algo parameter | หมายเหตุ |
|---|---|---|---|
| `aur` | `{"rules":["segwit"]}`, `"sha256d"` | ส่ง | Auroracoin multi-algo (`sha256d`/`scrypt`/`groestl`/`skein`/`qubit`) |
| `dgb` | `{"rules":["segwit"]}`, `"sha256d"` | ส่ง | DigiByte multi-algo (`+ odo`); ถ้า node ไม่รับพารามิเตอร์จะ retry แบบไม่ส่งให้อัตโนมัติ |
| `btc` | `{"rules":["segwit"]}` | ไม่ส่ง | Bitcoin และ fork ที่เหมือนกัน (algo เดียว) |
| *(ไม่ตั้ง)* | `{"rules":["segwit"]}` | ไม่ส่ง | generic — พฤติกรรมเดิมก่อนมี profile |

นอกจาก profile แล้ว pool ยังปรับตัวตาม node ตอน runtime:

- node ที่บังคับให้ client ประกาศ rule ที่ active (เช่น `Support for 'digidollar-oracle' rule requires explicit client support`) → เพิ่ม rule นั้นแล้วขอ template ใหม่ให้เอง
- node ที่ไม่รับ `rules:["segwit"]` (BCH/BSV) → fallback เป็น request ว่าง
- template ที่มี `pow_algo` ไม่ตรงกับ `RPC_ALGO` → log `WARNING` ว่า block นั้น hash ด้วย algo ที่ pool ไม่รองรับ

### ตัวอย่าง .env

Auroracoin:

```env
COIN=AUR
COIN_SYMBOL=AUR
RPC_HOST=192.168.99.254
RPC_PORT=12341
RPC_ALGO=sha256d
ZMQ_HOST=192.168.99.254
ZMQ_PORT=28332
WALLET_ADDRESS=AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr
```

DigiByte:

```env
COIN=DGB
COIN_SYMBOL=DGB
RPC_HOST=192.168.99.254
RPC_PORT=14022
RPC_ALGO=sha256d
ZMQ_HOST=192.168.99.254
ZMQ_PORT=28332
WALLET_ADDRESS=DChangeMeToYourDGBAddress
```

**`RPC_ALGO` ต้องเป็น `sha256d` เท่านั้น** — pool จะหยุดทำงานทันที (exit) ถ้าตั้งเป็น algo อื่น เพราะ build นี้ hash block header ด้วย SHA-256d อย่างเดียว ถ้าได้ template ที่ node บอกว่าเป็น algo อื่น (`pow_algo`) pool จะไม่ส่งงานนั้นให้ miner และคง job เดิมไว้

### Coinbase witness

- `aur`: node คืน `default_witness_commitment` มา แต่ validation ยังปฏิเสธ witness data (`unexpected-witness`) → pool ใส่ **output commitment** ตามปกติ แต่ **ไม่ใส่** 32-byte reserved value
- `dgb`, `btc`: ใส่ทั้ง commitment และ reserved value
- ถ้า node reject ด้วยเหตุผลเรื่อง witness ระบบจะ retry แบบไม่มี reserved value ให้ทันที และ**จำไว้จนกว่าจะ restart** (บล็อกถัด ๆ ไปจะไม่เสีย RPC ยิง reject อีก) — header/PoW ไม่เปลี่ยนเพราะ merkle root สร้างจาก coinbase txid

## Running Locally

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

```bash
docker compose up -d --build
docker compose logs -f ntpool
docker compose down
```

ถ้า Bitcoin node อยู่บน host machine อาจต้องเปิด `extra_hosts` ใน [docker-compose.yml](docker-compose.yml)

### RPC Troubleshooting

หากพบ `unexpected end of JSON input` อาจเกิดจาก node หรือ proxy ส่ง body ว่างหรือ JSON ไม่ครบ ไม่ได้หมายความว่าเชื่อมต่อ RPC สำเร็จแล้ว ข้อผิดพลาดจาก RPC client จะแสดงชื่อ method และ HTTP status เพื่อช่วยแยกสาเหตุ:

- `401 Unauthorized`: ตรวจ `RPC_USER` และ `RPC_PASSWORD` ให้ตรงกับ node
- `403 Forbidden`: ตรวจ `rpcallowip` และกฎการเข้าถึง RPC ให้ยอมรับ IP ของเครื่องหรือ container ที่รัน pool โดยไม่เปิด RPC สู่สาธารณะ
- `empty response body` หรือ `invalid JSON response`: ตรวจ `RPC_HOST` และ `RPC_PORT` ว่าชี้ไปยัง HTTP JSON-RPC ไม่ใช่พอร์ต P2P, Stratum หรือ ZMQ รวมถึงตรวจ log ของ node และ proxy
- หากรัน pool ใน Docker ค่า `127.0.0.1` จะหมายถึง container ของ pool เอง ไม่ใช่เครื่อง host ให้ใช้ชื่อ service ของ node หรือ host ที่ container เข้าถึงได้
- หากมีข้อความ RPC error จาก node เช่นกำลังโหลด block index ให้ตรวจสถานะ node ตามข้อความนั้น
- ถ้า log ขึ้น `[JobEngine Go] getblocktemplate failed (...)` แปลว่า node ปฏิเสธคำขอ template เอง (เช่น node บังคับให้ client ประกาศ rule ที่ active, `RPC_ALGO` ไม่ถูกต้อง หรือ node ยัง sync ไม่เสร็จ) — ข้อความจะขึ้นครั้งเดียวต่อ error แต่ละแบบ และจะขึ้น `getblocktemplate recovered` เมื่อกลับมาปกติ

### submitblock Results

`submitblock` คืน `nil` = node รับบล็อกแล้ว ค่าอื่นคือเหตุผลที่ปฏิเสธ ก่อนยิง RPC ทุกครั้ง pool จะ log บรรทัดสรุป:

```text
[Block] Submitting block #5926373 (0000…215e69): txs=0 coinbaseWitness=false witnessCommitment=6a24aa21a9ed…
[RPC submitblock] Result: <nil> (err: <nil>)
```

| result | ความหมาย | สิ่งที่ควรทำ |
|---|---|---|
| `nil` | รับบล็อกแล้ว | บันทึก blocks found + แจ้งเตือนอัตโนมัติ |
| `duplicate` / `duplicate-invalid` | มีบล็อกนี้แล้ว หรือเคยถูกปฏิเสธ | ไม่ต้องทำอะไร |
| `high-hash` | PoW ไม่ผ่าน target ของ algo นั้น | ตรวจว่า template เป็น algo ที่ `RPC_ALGO` ระบุ (multi-algo) |
| `bad-txnmrklroot` | merkle root ไม่ตรงกับ tx ที่ส่ง | ตรวจ log `[Job] Witness commitment …` และลำดับ tx |
| `bad-witness-nonce-size` | มี commitment แต่ reserved value ผิดขนาด | ไม่ควรเกิดแล้ว (pool ใส่ให้เอง) — แจ้ง bug พร้อม log |
| `unexpected-witness` | block มี witness data แต่ chain ไม่รับ | pool retry แบบไม่มี reserved value และปิดให้เองจน restart |
| `inconclusive` | node ยังไม่ตัดสิน (มักเป็น race) | เช็ก explorer ก่อนสรุปว่าไม่ผ่าน |

chain ที่ไม่รับ witness data (Auroracoin) จะมีบรรทัดนี้ครั้งเดียวตอนเจอบล็อกแรก แล้วบล็อกถัดไปจะไม่เสีย RPC ยิงซ้ำ:

```text
[Block] Node rejects witness data (result: unexpected-witness); retrying block #… without the coinbase witness reserved value and disabling it for this chain
```

## Runtime Notes

- Stratum server ฟังที่ `0.0.0.0:STRATUM_PORT`
- Web dashboard ฟังที่ `0.0.0.0:WEB_PORT` แต่มี middleware จำกัดการเข้าถึงเฉพาะ loopback / private LAN
- Dashboard ใช้ WebSocket เพื่อ push stats แบบ realtime
- เมื่อ `submitblock` สำเร็จ ระบบจะส่งการแจ้งเตือนตาม `ENABLE_NTFY` และ `ENABLE_DISCORD` (Discord ต้องตั้ง webhook URL ด้วย) และ reset best share ของทุก session
- รายการ blocks found ถูกเก็บไว้ใน `data/found_blocks.json`
- template ของ block จะถูกตรวจซ้ำทุกไม่กี่วินาที และ broadcast job ใหม่เฉพาะเมื่อเนื้อหาของ template เปลี่ยนจริง (ไม่ใช่ทุกครั้งที่ `curtime` เปลี่ยน) เพื่อไม่ให้ miner ถูก reset งานโดยไม่จำเป็น
- witness commitment ของ coinbase ถูกคำนวณจากรายการ tx ที่จะส่งจริงตาม BIP141 ส่วนค่าที่ node ส่งมาใน template ใช้เป็นเพียง hint; ถ้าค่าดังกล่าวไม่ใช่สคริปต์ commitment ที่ถูกต้องหรือไม่ตรงกับที่คำนวณได้ จะมี log `[Job] Witness commitment …` แล้วใช้ค่าที่คำนวณเอง
- chain ที่ validation ไม่รับ witness data (Auroracoin) จะไม่ได้ 32-byte reserved value ใน coinbase — ยังคงมี commitment output ตามปกติ ถ้า node reject ด้วยเหตุผลเรื่อง witness ระบบจะ retry ทันทีและปิด reserved value ให้จนกว่าจะ restart
- บน chain multi-algo (`aur`, `dgb`) template จะถูกขอตาม `RPC_ALGO` และถ้า node คืน `pow_algo` ไม่ตรงกันจะมี `WARNING` ใน log เพราะ block นั้น hash ด้วย algo ที่ pool ไม่รองรับ
- `STRATUM_IDLE_TIMEOUT` (วินาที, ค่าเริ่มต้น `0` = ปิด) ใช้ตัด connection ที่ไม่ส่งข้อมูลเข้ามาเลยตามเวลาที่กำหนด
- `STRATUM_MAX_CONNS` (ค่าเริ่มต้น `0` = ไม่จำกัด) ใช้จำกัดจำนวน connection ที่ active พร้อมกัน
- ตอนปิดโปรแกรม (Ctrl+C / SIGTERM) ระบบจะหยุดรับ connection ใหม่ ปิด session ของ miner และ shutdown web dashboard ให้เรียบร้อยก่อนจบ process

### Share Log

ทุก share ที่ถูก reject จะถูก log เสมอ เพื่อดูสาเหตุและ difficulty ที่ใช้ตัดสิน ส่วน share ที่ accept จะ log เฉพาะเมื่อตั้ง `LOG_SHARES=true`:

```text
[Share] REJECT worker=s21-01 ip=192.168.1.42 session=00000001 job=04af height=875123 reason="Low difficulty share" requiredDiff=4096 achievedDiff=512.00 jobAge=3s candidates=ext2:1 ntime:77 nonce:1 version:1 currentJob=04b2 jobsBehind=3
[Share] accept worker=s21-01 ip=192.168.1.42 session=00000001 job=04af height=875123 requiredDiff=4096 achievedDiff=5120.00
```

- `requiredDiff` คือ difficulty ที่ pool ใช้ตัดสิน ณ ตอนนั้น (อาจเป็น `PreviousDiff` ชั่วคราวหาก vardiff เพิ่งลด diff) และ `achievedDiff` คือ diff ที่ share นั้นทำได้จริง
- `reason` เป็นข้อความที่ส่งกลับไปให้ miner: `Low difficulty share`, `Duplicate share`, `Stale / Job not found`, หรือเหตุผลจากการบล็อก worker
- `jobAge` คืออายุของ job ที่ miner ใช้ส่งงาน (แสดง `n/a` เมื่อไม่พบ job) และ `currentJob`/`jobsBehind` จะแสดงเฉพาะกรณีส่ง job เก่ามาที่ server ไม่รู้จัก
- miner ที่ขุดด้วย diff ไม่ตรงกับที่ pool กำหนดจะเป็นสาเหตุหลักของ `Low difficulty share`; pool รับ `mining.suggest_difficulty` แล้วตอบรับ แต่ยังคงใช้ diff ของตัวเอง และจะ log ค่าที่ miner เสนอมาให้เห็น

## Connecting Miners

- URL: `stratum+tcp://<SERVER_IP>:3333`
- Username: `<WALLET_ADDRESS>.<WORKER_NAME>` หรือ `<ANY_PREFIX>.<WORKER_NAME>`
- Password: `x`

หมายเหตุ: โปรเจกต์นี้จะใช้ `WALLET_ADDRESS` จาก config เป็นปลายทาง payout เสมอ ส่วน prefix หน้า `.` จะถูกละทิ้ง และใช้เฉพาะชื่อหลังจุดเป็น worker name ที่แสดงใน dashboard เท่านั้น

ตัวอย่าง:

```text
anyprefix.s21-01
```

## Validation

```bash
go build ./...
go vet ./...
go test ./...   # ไฟล์ *_test.go ถูก gitignore ไว้ ให้เก็บไว้ในเครื่อง
```

## Changelog

| version | สรุป |
|---|---|
| `1.2.2` | Auroracoin: ไม่ใส่ coinbase witness reserved value (node ปฏิเสธ witness data) — ถ้าโดน reject ระบบจะ retry และปิดให้เองจน restart |
| `1.2.1` | ส่ง mining algorithm ให้ node multi-algo (`aur`, `dgb`) — ก่อนหน้านี้ node ใช้ `miningalgo` ของตัวเองซึ่งอาจไม่ใช่ sha256d |
| `1.2.0` | coin profile `COIN=aur/dgb/btc`, log `getblocktemplate` ที่เคยถูกกลืน, เตือนเมื่อ `pow_algo` ไม่ตรง |
| `1.1.1` | คำนวณ witness commitment เองจาก tx ที่จะส่งจริง แทนการเชื่อถือค่าจาก template |
| `1.1.0` | ใส่ coinbase witness nonce สำหรับบล็อก segwit |
| `1.0.5` | Discord notification และ market quote บน dashboard |

ตั้งแต่ `1.2.2` เป็นต้นไป pool ถือว่า **SHA-256d only**: `RPC_ALGO` อื่นจะทำให้โปรแกรมหยุดทันที และ template ที่ไม่ใช่ sha256d จะไม่ถูก broadcast

## License

MIT
