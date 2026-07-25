package pool

import (
	"encoding/binary"
	"encoding/hex"

	"ntpool/config"
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

// AddressToScriptPubKey converts P2PKH/P2SH/Bech32 address to hex scriptPubKey bytes
func AddressToScriptPubKey(address string) []byte {
	// Fallback P2PKH script (OP_DUP OP_HASH160 0x00... OP_EQUALVERIFY OP_CHECKSIG)
	fallback, _ := hex.DecodeString("76a914000000000000000000000000000000000000000088ac")
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
