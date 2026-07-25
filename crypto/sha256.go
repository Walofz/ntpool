package crypto

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"strconv"
)

// Diff1Target BigInt constant (0x00000000ffff0000000000000000000000000000000000000000000000000000)
var Diff1Target *big.Int

func init() {
	Diff1Target = new(big.Int)
	Diff1Target.SetString("00000000ffff0000000000000000000000000000000000000000000000000000", 16)
}

// Sha256d performs double SHA-256 (SHA-256(SHA-256(data)))
func Sha256d(data []byte) []byte {
	h1 := sha256.Sum256(data)
	h2 := sha256.Sum256(h1[:])
	return h2[:]
}

// ReverseBytes returns a new byte slice with reversed byte order
func ReverseBytes(src []byte) []byte {
	res := make([]byte, len(src))
	for i := 0; i < len(src); i++ {
		res[i] = src[len(src)-1-i]
	}
	return res
}

// NbitsToTarget converts compact nBits hex string to 256-bit BigInt target
func NbitsToTarget(nbitsHex string) *big.Int {
	nbits, err := strconv.ParseUint(nbitsHex, 16, 32)
	if err != nil {
		return big.NewInt(0)
	}
	exponent := uint(nbits >> 24)
	mantissa := big.NewInt(int64(nbits & 0x00ffffff))

	target := new(big.Int)
	if exponent <= 3 {
		target.Rsh(mantissa, 8*(3-exponent))
	} else {
		target.Lsh(mantissa, 8*(exponent-3))
	}
	return target
}

// NbitsToDifficulty converts nBits hex string to network difficulty
func NbitsToDifficulty(nbitsHex string) float64 {
	target := NbitsToTarget(nbitsHex)
	if target.Sign() == 0 {
		return 0
	}
	diff1Float := new(big.Float).SetInt(Diff1Target)
	targetFloat := new(big.Float).SetInt(target)
	resFloat := new(big.Float).Quo(diff1Float, targetFloat)
	res, _ := resFloat.Float64()
	return res
}

// DifficultyToTarget converts numeric difficulty to 256-bit target BigInt
func DifficultyToTarget(diff float64) *big.Int {
	if diff <= 0 {
		return new(big.Int).Set(Diff1Target)
	}
	diff1Float := new(big.Float).SetInt(Diff1Target)
	diffFloat := big.NewFloat(diff)
	targetFloat := new(big.Float).Quo(diff1Float, diffFloat)

	target := new(big.Int)
	targetFloat.Int(target)
	return target
}

// HashToDifficulty calculates achieved difficulty score from double-sha256 header hash LE
func HashToDifficulty(hashLE []byte) float64 {
	hashBE := ReverseBytes(hashLE)
	hashBigInt := new(big.Int).SetBytes(hashBE)
	if hashBigInt.Sign() == 0 {
		return math.MaxFloat64
	}
	diff1Float := new(big.Float).SetInt(Diff1Target)
	hashFloat := new(big.Float).SetInt(hashBigInt)
	resFloat := new(big.Float).Quo(diff1Float, hashFloat)
	res, _ := resFloat.Float64()
	return res
}

// CalculateMerkleRoot computes Merkle Root from Coinbase TxID (32-byte BE) and Merkle Branch Array
func CalculateMerkleRoot(coinbaseTxidBE []byte, merkleBranchHex []string) []byte {
	currentHash := coinbaseTxidBE

	for _, branchHex := range merkleBranchHex {
		branchBuf, err := hex.DecodeString(branchHex)
		if err != nil {
			continue
		}
		combined := append(currentHash, branchBuf...)
		currentHash = Sha256d(combined)
	}

	return currentHash
}

// BuildBlockHeader constructs an 80-byte Bitcoin Block Header Buffer
func BuildBlockHeader(version uint32, prevHashRawHex string, merkleRootBE []byte, nTimeHex, nBitsHex, nonceHex string) []byte {
	header := make([]byte, 80)

	// 1. Version (uint32 LE)
	binary.LittleEndian.PutUint32(header[0:4], version)

	// 2. PrevHash (32 bytes LE reversed from RPC BE)
	prevHashBE, _ := hex.DecodeString(prevHashRawHex)
	copy(header[4:36], ReverseBytes(prevHashBE))

	// 3. Merkle Root (32 bytes)
	copy(header[36:68], merkleRootBE)

	// 4. nTime (uint32 LE)
	nTimeVal, _ := strconv.ParseUint(nTimeHex, 16, 32)
	binary.LittleEndian.PutUint32(header[68:72], uint32(nTimeVal))

	// 5. nBits (uint32 LE)
	nBitsVal, _ := strconv.ParseUint(nBitsHex, 16, 32)
	binary.LittleEndian.PutUint32(header[72:76], uint32(nBitsVal))

	// 6. Nonce (uint32 LE)
	nonceVal, _ := strconv.ParseUint(nonceHex, 16, 32)
	binary.LittleEndian.PutUint32(header[76:80], uint32(nonceVal))

	return header
}
