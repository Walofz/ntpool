package pool

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"strings"

	"zpoolproxy/config"
)

type CoinbaseParts struct {
	Coinb1 string
	Coinb2 string
}

// EncodeBip34Height encodes block height into BIP34 push script bytes
func EncodeBip34Height(height int64) []byte {
	if height == 0 {
		return []byte{0x01, 0x00}
	}
	var bytes []byte
	temp := height
	for temp > 0 {
		bytes = append(bytes, byte(temp&0xff))
		temp >>= 8
	}
	if bytes[len(bytes)-1]&0x80 != 0 {
		bytes = append(bytes, 0x00)
	}
	return append([]byte{byte(len(bytes))}, bytes...)
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58Decode(input string) []byte {
	radix := big.NewInt(58)
	result := big.NewInt(0)

	for i := 0; i < len(input); i++ {
		char := input[i]
		idx := strings.IndexByte(base58Alphabet, char)
		if idx == -1 {
			return nil
		}
		result.Mul(result, radix)
		result.Add(result, big.NewInt(int64(idx)))
	}

	b := result.Bytes()

	// Count leading '1's
	var numZeros int
	for i := 0; i < len(input) && input[i] == '1'; i++ {
		numZeros++
	}

	res := make([]byte, numZeros+len(b))
	copy(res[numZeros:], b)
	return res
}

func bech32DecodeToScriptPubKey(address string) []byte {
	clean := strings.ToLower(strings.TrimSpace(address))
	parts := strings.Split(clean, "1")
	if len(parts) != 2 {
		return nil
	}
	dataPart := parts[1]
	if len(dataPart) < 6 {
		return nil
	}

	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	var values []byte
	for i := 0; i < len(dataPart)-6; i++ {
		idx := strings.IndexByte(charset, dataPart[i])
		if idx == -1 {
			return nil
		}
		values = append(values, byte(idx))
	}

	if len(values) == 0 {
		return nil
	}

	witnessVersion := values[0]
	program5bit := values[1:]

	// Convert 5-bit array to 8-bit buffer
	acc := 0
	bits := 0
	var program8bit []byte
	for _, val := range program5bit {
		acc = (acc << 5) | int(val)
		bits += 5
		for bits >= 8 {
			bits -= 8
			program8bit = append(program8bit, byte((acc>>bits)&0xff))
		}
	}

	var witnessOp byte
	if witnessVersion == 0 {
		witnessOp = 0x00
	} else {
		witnessOp = 0x50 + witnessVersion
	}

	var script []byte
	script = append(script, witnessOp, byte(len(program8bit)))
	script = append(script, program8bit...)
	return script
}

// AddressToScriptPubKey converts P2PKH/P2SH/Bech32 address or raw Hex script to hex scriptPubKey bytes
func AddressToScriptPubKey(address string) []byte {
	clean := strings.TrimSpace(address)
	fallback, _ := hex.DecodeString("76a914000000000000000000000000000000000000000088ac")

	if clean == "" {
		return fallback
	}

	// 0. Raw Hex ScriptPubKey (e.g. 76a914...88ac or a914...87 or 0014...)
	if rawBytes, err := hex.DecodeString(clean); err == nil && len(rawBytes) >= 10 {
		return rawBytes
	}

	// 1. Bech32 / Segwit (bc1... / tb1... / bcrt1... / dgb1... / ltc1... / any string containing '1')
	if strings.Contains(clean, "1") {
		script := bech32DecodeToScriptPubKey(clean)
		if script != nil {
			return script
		}
	}

	// 2. Base58 (P2PKH / P2SH for any SHA-256 altcoin)
	decoded := base58Decode(clean)
	if len(decoded) >= 25 {
		payload := decoded[len(decoded)-24 : len(decoded)-4] // Extract 20-byte hash payload
		versionByte := decoded[0]

		// P2SH (version 0x05 / 0xc4 / 0x3f)
		if versionByte == 0x05 || versionByte == 0xc4 || versionByte == 0x3f {
			var script []byte
			prefix, _ := hex.DecodeString("a914")
			suffix, _ := hex.DecodeString("87")
			script = append(script, prefix...)
			script = append(script, payload...)
			script = append(script, suffix...)
			return script
		}

		// P2PKH (Universal for Bitcoin '1', DigiByte 'A'/'D', Bitcoin Cash, BSV, Luckycoin, Pepecoin, Litecoin, Testnet)
		var script []byte
		prefix, _ := hex.DecodeString("76a914")
		suffix, _ := hex.DecodeString("88ac")
		script = append(script, prefix...)
		script = append(script, payload...)
		script = append(script, suffix...)
		return script
	}

	return fallback
}

// BuildCoinbaseTransaction constructs the coinb1 and coinb2 hex strings
func BuildCoinbaseTransaction(
	cfg *config.Config,
	blockHeight int64,
	coinbaseValue int64,
	minerAddress string,
	extranonce1Size int,
	extranonce2Size int,
	defaultWitnessCommitment string,
) CoinbaseParts {
	versionBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(versionBuf, 1)

	inputCountBuf := []byte{0x01}
	prevTxIdBuf := make([]byte, 32)
	prevOutIdxBuf, _ := hex.DecodeString("ffffffff")

	heightPushBuf := EncodeBip34Height(blockHeight)
	totalExtranonceSize := extranonce1Size + extranonce2Size
	scriptLen := len(heightPushBuf) + totalExtranonceSize
	scriptLenBuf := []byte{byte(scriptLen)}
	sequenceBuf, _ := hex.DecodeString("ffffffff")

	// COINB1
	var coinb1Buf []byte
	coinb1Buf = append(coinb1Buf, versionBuf...)
	coinb1Buf = append(coinb1Buf, inputCountBuf...)
	coinb1Buf = append(coinb1Buf, prevTxIdBuf...)
	coinb1Buf = append(coinb1Buf, prevOutIdxBuf...)
	coinb1Buf = append(coinb1Buf, scriptLenBuf...)
	coinb1Buf = append(coinb1Buf, heightPushBuf...)

	// OUTPUTS
	var feeSatoshis int64 = 0
	if cfg.PoolFeePercent > 0 && cfg.PoolFeeAddress != "" {
		feeSatoshis = int64(float64(coinbaseValue) * (cfg.PoolFeePercent / 100.0))
	}
	minerSatoshis := coinbaseValue - feeSatoshis

	scriptPubKey := AddressToScriptPubKey(minerAddress)

	minerValueBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(minerValueBuf, uint64(minerSatoshis))
	scriptPubKeyLenBuf := []byte{byte(len(scriptPubKey))}

	var minerOutputBuf []byte
	minerOutputBuf = append(minerOutputBuf, minerValueBuf...)
	minerOutputBuf = append(minerOutputBuf, scriptPubKeyLenBuf...)
	minerOutputBuf = append(minerOutputBuf, scriptPubKey...)

	var feeOutputBuf []byte
	hasFeeOutput := false
	if feeSatoshis > 0 && cfg.PoolFeeAddress != "" {
		feeScriptPubKey := AddressToScriptPubKey(cfg.PoolFeeAddress)
		feeValueBuf := make([]byte, 8)
		binary.LittleEndian.PutUint64(feeValueBuf, uint64(feeSatoshis))
		feeScriptLenBuf := []byte{byte(len(feeScriptPubKey))}

		feeOutputBuf = append(feeOutputBuf, feeValueBuf...)
		feeOutputBuf = append(feeOutputBuf, feeScriptLenBuf...)
		feeOutputBuf = append(feeOutputBuf, feeScriptPubKey...)
		hasFeeOutput = true
	}

	var witnessOutputBuf []byte
	if defaultWitnessCommitment != "" {
		witnessValueBuf := make([]byte, 8)
		commitmentBuf, err := hex.DecodeString(defaultWitnessCommitment)
		if err == nil {
			commitmentLenBuf := []byte{byte(len(commitmentBuf))}
			witnessOutputBuf = append(witnessOutputBuf, witnessValueBuf...)
			witnessOutputBuf = append(witnessOutputBuf, commitmentLenBuf...)
			witnessOutputBuf = append(witnessOutputBuf, commitmentBuf...)
		}
	}

	totalOutputs := 1
	if hasFeeOutput {
		totalOutputs++
	}
	if len(witnessOutputBuf) > 0 {
		totalOutputs++
	}
	outputCountBuf := []byte{byte(totalOutputs)}
	lockTimeBuf := make([]byte, 4)

	// COINB2
	var coinb2Buf []byte
	coinb2Buf = append(coinb2Buf, sequenceBuf...)
	coinb2Buf = append(coinb2Buf, outputCountBuf...)
	coinb2Buf = append(coinb2Buf, minerOutputBuf...)
	coinb2Buf = append(coinb2Buf, feeOutputBuf...)
	coinb2Buf = append(coinb2Buf, witnessOutputBuf...)
	coinb2Buf = append(coinb2Buf, lockTimeBuf...)

	return CoinbaseParts{
		Coinb1: hex.EncodeToString(coinb1Buf),
		Coinb2: hex.EncodeToString(coinb2Buf),
	}
}
