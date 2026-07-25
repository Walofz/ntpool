import crypto from 'crypto';

/**
 * Standard Bitcoin SHA-256d (Double SHA-256)
 */
export function sha256d(data: Buffer): Buffer {
  const hash1 = crypto.createHash('sha256').update(data).digest();
  return crypto.createHash('sha256').update(hash1).digest();
}

/**
 * Reverse a Buffer (endianness swap)
 */
export function reverseBuffer(buffer: Buffer): Buffer {
  const res = Buffer.allocUnsafe(buffer.length);
  for (let i = 0; i < buffer.length; i++) {
    res[i] = buffer[buffer.length - 1 - i];
  }
  return res;
}

/**
 * Convert compact nBits representation to 256-bit BigInt target
 */
export function nbitsToTarget(nbitsHex: string): bigint {
  const nbits = parseInt(nbitsHex, 16);
  const exponent = nbits >> 24;
  const mantissa = BigInt(nbits & 0x00ffffff);
  if (exponent <= 3) {
    return mantissa >> BigInt(8 * (3 - exponent));
  }
  return mantissa << BigInt(8 * (exponent - 3));
}

/**
 * Difficulty 1 Target (0x00000000ffff0000000000000000000000000000000000000000000000000000)
 */
export const DIFF1_TARGET = 0x00000000ffff0000000000000000000000000000000000000000000000000000n;

/**
 * Convert numeric difficulty to 256-bit target BigInt
 */
export function difficultyToTarget(diff: number): bigint {
  if (diff <= 0) return DIFF1_TARGET;
  const target = BigInt(Math.floor(Number(DIFF1_TARGET) / diff));
  return target;
}

/**
 * Calculate achieved difficulty score from double-sha256 header hash Buffer (32 bytes)
 */
export function hashToDifficulty(hashLE: Buffer): number {
  const hashBE = reverseBuffer(hashLE);
  const hashBigInt = BigInt('0x' + hashBE.toString('hex'));
  if (hashBigInt === 0n) return Number.MAX_SAFE_INTEGER;
  return Number(DIFF1_TARGET) / Number(hashBigInt);
}

/**
 * Convert Buffer or hex to 256-bit BigInt for comparison
 */
export function bufferToBigIntBE(buffer: Buffer): bigint {
  return BigInt('0x' + buffer.toString('hex'));
}

/**
 * Calculate Merkle Root from Coinbase TxID (32-byte BE) and Merkle Branch Array (strings/Buffers)
 */
export function calculateMerkleRoot(coinbaseTxidBE: Buffer, merkleBranchHexArray: string[]): Buffer {
  let currentHash = coinbaseTxidBE;

  for (const branchHex of merkleBranchHexArray) {
    const branchBuf = Buffer.from(branchHex, 'hex');
    // Concatenate currentHash + branchBuf
    const combined = Buffer.concat([currentHash, branchBuf]);
    currentHash = sha256d(combined);
  }

  return currentHash;
}

/**
 * Reconstruct Header Version for AsicBoost (Version Rolling)
 * Formula: (baseVersion & ~mask) | (versionBits & mask)
 */
export function calculateAsicBoostVersion(
  baseVersionHex: string,
  versionBitsHex?: string,
  maskHex?: string
): number {
  const baseVersion = parseInt(baseVersionHex, 16);
  if (!versionBitsHex || !maskHex) {
    return baseVersion;
  }
  const versionBits = parseInt(versionBitsHex, 16);
  const mask = parseInt(maskHex, 16);

  // Bitwise composition
  const finalVersion = (baseVersion & ~mask) | (versionBits & mask);
  // Ensure unsigned 32-bit integer
  return finalVersion >>> 0;
}

/**
 * Construct 80-byte Bitcoin Block Header Buffer
 */
export function buildBlockHeader(params: {
  version: number;
  prevHashHex: string;
  merkleRootBE: Buffer;
  nTimeHex: string;
  nBitsHex: string;
  nonceHex: string;
}): Buffer {
  const header = Buffer.alloc(80);

  // 1. Version (4 bytes, Little Endian)
  header.writeUInt32LE(params.version, 0);

  // 2. PrevHash (32 bytes, Little Endian - input prevHashHex from stratum is in reversed 4-byte chunks or LE hex)
  // Standard Stratum prevhash is encoded as 8 x 4-byte LE words or raw 32-byte LE hex
  const prevHashBuf = Buffer.from(params.prevHashHex, 'hex');
  if (prevHashBuf.length === 32) {
    prevHashBuf.copy(header, 4);
  } else {
    // Fallback swap if needed
    Buffer.alloc(32).copy(header, 4);
  }

  // 3. Merkle Root (32 bytes, Little Endian)
  // Note: calculateMerkleRoot gives LE txid hashes, copy LE
  params.merkleRootBE.copy(header, 36);

  // 4. nTime (4 bytes, Little Endian)
  const nTime = parseInt(params.nTimeHex, 16);
  header.writeUInt32LE(nTime, 68);

  // 5. nBits (4 bytes, Little Endian)
  const nBits = parseInt(params.nBitsHex, 16);
  header.writeUInt32LE(nBits, 72);

  // 6. Nonce (4 bytes, Little Endian)
  const nonce = parseInt(params.nonceHex, 16);
  header.writeUInt32LE(nonce, 76);

  return header;
}
