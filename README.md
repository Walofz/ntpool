# ⚡ ntpool (Node Type Pool)

High-performance SHA-256 Solo Mining Pool (CKPool style architecture) with Overt AsicBoost support and real-time Web Dashboard. Built with TypeScript & Node.js.

---

## 🌟 Overview & Key Features

**ntpool** (Node Type Pool) คือระบบ Solo Mining Pool ประสิทธิภาพสูงที่ถูกออกแบบตามสถาปัตยกรรม CKPool สำหรับการขุด Bitcoin (SHA-256) แบบพึ่งพา Node ตัวเอง (Node Type Pool) โดยเน้นความเร็ว ความแม่นยำ และรองรับเครื่องขุด ASIC สมัยใหม่เต็มรูปแบบ

- **⚡ CKPool Style Architecture**: คำนวณ Work และแจกจ่าย Job ให้อย่างรวดเร็วผ่าน Stratum Protocol V1
- **🚀 Overt AsicBoost (Version Rolling)**: รองรับการขุดแบบ AsicBoost ช่วยเพิ่มประสิทธิภาพและประหยัดพลังงานสำหรับเครื่อง ASIC (เช่น Antminer S9/S19/S21, Whatsminer, Avalon)
- **💎 Direct Coinbase Payout**: เมื่อขุดเจอ บล็อกและรางวัลรวมถึงค่าธรรมเนียมธุรกรรม (Block Reward + Tx Fees) จะถูกจ่ายตรงไปยัง Bitcoin Address ของผู้ขุดใน Coinbase Transaction ทันที ไม่ต้องผ่านกระเป๋าของ Pool
- **📊 Real-time Web Dashboard**: ดูสถานะเครื่องขุด (Workers), Hashrate 1m/5m, Diff, Best Share และค้นหาข้อมูลตาม Bitcoin Address ผ่าน Web Browser แบบเรียลไทม์ (WebSocket)
- **🐳 Docker Ready**: มี `Dockerfile` และ `docker-compose.yml` สำหรับการติดตั้งและรันแบบ Container บน Server ได้อย่างสะดวก

---

## 📁 Project Structure

```
ntpool/
├── src/
│   ├── index.ts              # Entry point ของระบบ
│   ├── config.ts             # โหลดและจัดการค่า Configuration จาก .env
│   ├── bitcoin/              # RPC & ZMQ client สำหรับเชื่อมต่อกับ Bitcoin Core Node
│   ├── pool/                 # Job Manager และการบริหารจัดการ Block Template
│   ├── stratum/              # Stratum TCP Server & Session handling
│   ├── web/                  # Web Dashboard Express & WebSocket Server
│   └── simulator/            # Mock Node & Miner สำหรับทดสอบระบบ
├── public/                   # Web Dashboard UI Frontend (HTML/CSS/JS)
├── dist/                     # JavaScript build output (เกิดจาก tsc)
├── Dockerfile                # Multi-stage Docker build configuration
├── docker-compose.yml        # Docker Compose configuration file
├── .env.example              # ตัวอย่างไฟล์การตั้งค่า Environment
└── README.md
```

---

## ⚙️ Environment Variables Configuration

คัดลอกไฟล์ `.env.example` เป็น `.env` และปรับแต่งค่าตามต้องการ:

```bash
cp .env.example .env
```

| ตัวแปร | รายละเอียด | ค่าเริ่มต้น |
| :--- | :--- | :--- |
| `STRATUM_PORT` | พอร์ตสำหรับเครื่องขุด ASIC เชื่อมต่อ Stratum | `3333` |
| `WEB_PORT` | พอร์ตสำหรับหน้าเว็บ Web Dashboard UI | `8080` |
| `DEFAULT_DIFF` | ค่า ความยาก (Difficulty) เริ่มต้นสำหรับ Miner | `1024` |
| `MIN_DIFF` | ค่า Difficulty ต่ำสุด (VarDiff) | `64` |
| `MAX_DIFF` | ค่า Difficulty สูงสุด (VarDiff) | `1048576` |
| `RPC_HOST` | IP/Hostname ของ Bitcoin Core Node RPC | `127.0.0.1` |
| `RPC_PORT` | พอร์ต RPC ของ Bitcoin Core Node | `8332` |
| `RPC_USER` | RPC Username ใน `bitcoin.conf` | `bitcoinrpc` |
| `RPC_PASSWORD` | RPC Password ใน `bitcoin.conf` | `rpcpassword` |
| `RPC_NETWORK` | เครือข่าย (`mainnet`, `testnet`, `regtest`) | `mainnet` |
| `ZMQ_HOST` | IP ของ ZMQ Server บน Bitcoin Node (Optional) | `127.0.0.1` |
| `ZMQ_PORT` | พอร์ต ZMQ rawblock สำหรับการอัปเดตบล็อกใหม่ทันที | `28332` |
| `POOL_NAME` | ชื่อเรียกของ Pool | `ntpool SHA-256 Solo Pool` |
| `COINBASE_TEXT` | ข้อความระบุตัวตนที่จะใส่ลงใน Coinbase Transaction | `/ntpool/` |

---

## 🚀 Quick Start (Local Development)

### 1. ติดตั้ง Dependencies

```bash
npm install
```

### 2. รันในโหมด Development (Hot-Reload)

```bash
npm run dev
```

### 3. Build & Run ในโหมด Production

```bash
# คอมไพล์ TypeScript เป็น JavaScript
npm run build

# เริ่มทำงาน Production Server
npm start
```

---

## 🐳 Running with Docker / Docker Compose

### ใช้งานด้วย Docker Compose (แนะนำ)

1. ตรวจสอบการตั้งค่าใน `.env`
2. สั่งรัน Container:

```bash
docker compose up -d --build
```

3. ตรวจสอบ Logs การทำงาน:

```bash
docker compose logs -f ntpool
```

4. หยุดการทำงาน:

```bash
docker compose down
```

### ใช้งานด้วย Docker CLI โดยตรง

```bash
# Build Docker Image
docker build -t ntpool .

# Run Container
docker run -d \
  --name ntpool \
  -p 3333:3333 \
  -p 8080:8080 \
  --env-file .env \
  ntpool
```

---

## ⛏️ Connecting Miners to Pool

ตั้งค่าเครื่องขุด ASIC หรือ Stratum Miner ซอฟต์แวร์ของคุณดังนี้:

- **URL / Host**: `stratum+tcp://<YOUR_SERVER_IP>:3333`
- **Worker / User**: `<YOUR_BITCOIN_ADDRESS>.<WORKER_NAME>`
  - *ตัวอย่าง*: `bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh.antminer_s19`
- **Password**: `x` (หรือปล่อยว่าง)

---

## 📄 License

MIT License
