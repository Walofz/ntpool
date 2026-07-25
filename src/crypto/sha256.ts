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
 * Convert compact nBits hex string to Network Difficulty number
 */
export function nbitsToDifficulty(nbitsHex: string): number {
  const target = nbitsToTarget(nbitsHex);
  if (target === 0n) return 0;
  return Number(DIFF1_TARGET) / Number(target);
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
  prevHashRawHex: string;
  merkleRootBE: Buffer;
  nTimeHex: string;
  nBitsHex: string;
  nonceHex: string;
  swapVersionBE?: boolean;
}): Buffer {
  const header = Buffer.alloc(80);

  // 1. Version (4 bytes LE or BE)
  if (params.swapVersionBE) {
    header.writeUInt32BE(params.version >>> 0, 0);
  } else {
    header.writeUInt32LE(params.version >>> 0, 0);
  }

  // 2. PrevHash (32 bytes reversed)
  reverseBuffer(Buffer.from(params.prevHashRawHex, 'hex')).copy(header, 4);

  // 3. Merkle Root (32 bytes LE)
  params.merkleRootBE.copy(header, 36);

  // 4. nTime (4 bytes reversed)
  reverseBuffer(Buffer.from(params.nTimeHex, 'hex')).copy(header, 68);

  // 5. nBits (4 bytes reversed)
  reverseBuffer(Buffer.from(params.nBitsHex, 'hex')).copy(header, 72);

  // 6. Nonce (4 bytes reversed)
  reverseBuffer(Buffer.from(params.nonceHex, 'hex')).copy(header, 76);

  return header;
}
