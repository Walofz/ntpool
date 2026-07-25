import crypto from 'crypto';
import { config } from '../config';

/**
 * Convert Bitcoin Address (P2PKH, P2SH, Bech32 Segwit) to Hex scriptPubKey
 */
export function addressToScriptPubKey(address: string): Buffer {
  const cleanAddr = address.trim();

  // 1. Bech32 / Bech32m Segwit (bc1... / tb1... / bcrt1...)
  if (cleanAddr.startsWith('bc1') || cleanAddr.startsWith('tb1') || cleanAddr.startsWith('bcrt1')) {
    return bech32ToScriptPubKey(cleanAddr);
  }

  // 2. Base58 (P2PKH / P2SH)
  const decoded = base58Decode(cleanAddr);
  if (!decoded || decoded.length < 25) {
    throw new Error(`Invalid Base58 Bitcoin Address: ${address}`);
  }

  const payload = decoded.subarray(1, 21); // 20-byte hash
  const versionByte = decoded[0];

  // P2PKH (Mainnet 0x00, Testnet/Regtest 0x6f) -> OP_DUP OP_HASH160 <20> OP_EQUALVERIFY OP_CHECKSIG
  if (versionByte === 0x00 || versionByte === 0x6f) {
    return Buffer.concat([
      Buffer.from('76a914', 'hex'),
      payload,
      Buffer.from('88ac', 'hex'),
    ]);
  }

  // P2SH (Mainnet 0x05, Testnet/Regtest 0xc4) -> OP_HASH160 <20> OP_EQUAL
  if (versionByte === 0x05 || versionByte === 0xc4) {
    return Buffer.concat([
      Buffer.from('a914', 'hex'),
      payload,
      Buffer.from('87', 'hex'),
    ]);
  }

  // Fallback P2PKH
  return Buffer.concat([
    Buffer.from('76a914', 'hex'),
    payload,
    Buffer.from('88ac', 'hex'),
  ]);
}

/**
 * Base58 decoding implementation
 */
const ALPHABET = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
const BASE58_MAP: Record<string, number> = {};
for (let i = 0; i < ALPHABET.length; i++) {
  BASE58_MAP[ALPHABET.charAt(i)] = i;
}

function base58Decode(str: string): Buffer | null {
  if (str.length === 0) return null;
  let bytes = [0];
  for (let i = 0; i < str.length; i++) {
    const char = str[i];
    const value = BASE58_MAP[char];
    if (value === undefined) return null;

    let carry = value;
    for (let j = 0; j < bytes.length; j++) {
      carry += bytes[j] * 58;
      bytes[j] = carry & 0xff;
      carry >>= 8;
    }

    while (carry > 0) {
      bytes.push(carry & 0xff);
      carry >>= 8;
    }
  }

  for (let i = 0; i < str.length && str[i] === '1'; i++) {
    bytes.push(0);
  }

  return Buffer.from(bytes.reverse());
}

/**
 * Basic Bech32 to scriptPubKey converter
 */
function bech32ToScriptPubKey(address: string): Buffer {
  const parts = address.toLowerCase().split('1');
  if (parts.length !== 2) throw new Error('Invalid Bech32 Address format');
  const dataPart = parts[1];

  const CHARSET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l';
  const decodedBytes: number[] = [];
  for (let i = 0; i < dataPart.length - 6; i++) {
    const idx = CHARSET.indexOf(dataPart[i]);
    if (idx === -1) throw new Error('Invalid Bech32 character');
    decodedBytes.push(idx);
  }

  const witnessVersion = decodedBytes[0];
  const program5bit = decodedBytes.slice(1);
  
  // Convert 5-bit array to 8-bit buffer
  let acc = 0;
  let bits = 0;
  const program8bit: number[] = [];
  for (const value of program5bit) {
    acc = (acc << 5) | value;
    bits += 5;
    while (bits >= 8) {
      bits -= 8;
      program8bit.push((acc >> bits) & 0xff);
    }
  }

  const programBuf = Buffer.from(program8bit);
  // OP_0 (0x00) + PushLength + Program
  const witnessOp = witnessVersion === 0 ? 0x00 : 0x50 + witnessVersion;
  return Buffer.concat([
    Buffer.from([witnessOp, programBuf.length]),
    programBuf,
  ]);
}

/**
 * Encode BIP34 Block Height into script push bytes
 */
function encodeBip34Height(height: number): Buffer {
  if (height === 0) return Buffer.from([0x01, 0x00]);
  const bytes: number[] = [];
  let temp = height;
  while (temp > 0) {
    bytes.push(temp & 0xff);
    temp >>= 8;
  }
  // Add positive sign byte if top bit is set
  if (bytes[bytes.length - 1] & 0x80) {
    bytes.push(0x00);
  }
  return Buffer.concat([Buffer.from([bytes.length]), Buffer.from(bytes)]);
}

export interface CoinbaseParts {
  coinb1: string;
  coinb2: string;
}

/**
 * Create CKPool Solo Coinbase Transaction split into coinb1 & coinb2
 */
export function buildCoinbaseTransaction(params: {
  blockHeight: number;
  coinbaseValue: number;
  minerAddress: string;
  extranonce1Size: number;
  extranonce2Size: number;
  defaultWitnessCommitment?: string;
}): CoinbaseParts {
  const versionBuf = Buffer.alloc(4);
  versionBuf.writeUInt32LE(1, 0); // Version 1

  const inputCountBuf = Buffer.from([0x01]); // 1 input

  const prevTxIdBuf = Buffer.alloc(32, 0); // 32 bytes of zeros
  const prevOutIdxBuf = Buffer.from('ffffffff', 'hex'); // 0xffffffff

  // Height push
  const heightPushBuf = encodeBip34Height(params.blockHeight);

  // Pool Signature text
  const poolTextBuf = Buffer.from(config.coinbaseText, 'utf8');

  // Part of coinbase script BEFORE extranonce1
  const scriptPrefix = Buffer.concat([heightPushBuf, poolTextBuf]);

  // Total extranonce length = extranonce1 + extranonce2
  const totalExtranonceSize = params.extranonce1Size + params.extranonce2Size;

  // Total script length = scriptPrefix length + totalExtranonceSize
  const scriptLen = scriptPrefix.length + totalExtranonceSize;
  const scriptLenBuf = Buffer.from([scriptLen]);

  const sequenceBuf = Buffer.from('ffffffff', 'hex');

  // COINB1 = Everything from Tx Version down to end of scriptPrefix
  const coinb1Buf = Buffer.concat([
    versionBuf,
    inputCountBuf,
    prevTxIdBuf,
    prevOutIdxBuf,
    scriptLenBuf,
    scriptPrefix,
  ]);

  // OUTPUTS
  let feeSatoshis = 0;
  if (config.poolFeePercent > 0 && config.poolFeeAddress) {
    feeSatoshis = Math.floor(params.coinbaseValue * (config.poolFeePercent / 100));
  }
  const minerSatoshis = params.coinbaseValue - feeSatoshis;

  let scriptPubKey: Buffer;
  try {
    scriptPubKey = addressToScriptPubKey(params.minerAddress);
  } catch (err) {
    // Fallback unspendable script if invalid address provided
    scriptPubKey = Buffer.from('76a914000000000000000000000000000000000000000088ac', 'hex');
  }

  const minerValueBuf = Buffer.alloc(8);
  minerValueBuf.writeBigUInt64LE(BigInt(minerSatoshis), 0);
  const scriptPubKeyLenBuf = Buffer.from([scriptPubKey.length]);
  const minerOutputBuf = Buffer.concat([minerValueBuf, scriptPubKeyLenBuf, scriptPubKey]);

  let feeOutputBuf = Buffer.alloc(0);
  let hasFeeOutput = false;
  if (feeSatoshis > 0 && config.poolFeeAddress) {
    try {
      const feeScriptPubKey = addressToScriptPubKey(config.poolFeeAddress);
      const feeValueBuf = Buffer.alloc(8);
      feeValueBuf.writeBigUInt64LE(BigInt(feeSatoshis), 0);
      const feeScriptLenBuf = Buffer.from([feeScriptPubKey.length]);
      feeOutputBuf = Buffer.concat([feeValueBuf, feeScriptLenBuf, feeScriptPubKey]);
      hasFeeOutput = true;
    } catch (e) {
      console.error('[Coinbase] Invalid POOL_FEE_ADDRESS in config, skipping fee output');
    }
  }

  let witnessOutputBuf = Buffer.alloc(0);
  if (params.defaultWitnessCommitment) {
    const witnessValueBuf = Buffer.alloc(8, 0); // 0 satoshis
    const commitmentBuf = Buffer.from(params.defaultWitnessCommitment, 'hex');
    const commitmentLenBuf = Buffer.from([commitmentBuf.length]);
    witnessOutputBuf = Buffer.concat([witnessValueBuf, commitmentLenBuf, commitmentBuf]);
  }

  let totalOutputs = 1;
  if (hasFeeOutput) totalOutputs++;
  if (params.defaultWitnessCommitment) totalOutputs++;
  const outputCountBuf = Buffer.from([totalOutputs]);

  const lockTimeBuf = Buffer.alloc(4, 0); // Locktime 0

  // COINB2 = Sequence + Output Count + Outputs + Locktime
  const coinb2Buf = Buffer.concat([
    sequenceBuf,
    outputCountBuf,
    minerOutputBuf,
    feeOutputBuf,
    witnessOutputBuf,
    lockTimeBuf,
  ]);

  return {
    coinb1: coinb1Buf.toString('hex'),
    coinb2: coinb2Buf.toString('hex'),
  };
}
