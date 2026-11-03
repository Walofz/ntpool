package pool

import (
	"encoding/binary"
	"encoding/hex"
	"log"
	"strings"

	"ntpool/crypto"
)

// WitnessCommitmentHeader is the OP_RETURN prefix every BIP141 witness
// commitment output starts with (OP_RETURN, push 36 bytes, 0xaa21a9ed).
const WitnessCommitmentHeader = "6a24aa21a9ed"

// witnessReservedValue is the 32-byte value AddWitnessNonce writes into the
// coinbase witness stack. The commitment is bound to it, so both places must
// always agree.
var witnessReservedValue = make([]byte, 32)

// TxHasWitness reports whether a serialized transaction carries witness data.
// Segregated serialization is version(4) | marker 0x00 | flag 0x01 | ...
// A legacy transaction can never have a zero input count, so a zero byte where
// the input count belongs is unambiguous.
func TxHasWitness(txHex string) bool {
	clean := strings.TrimSpace(txHex)
	if len(clean) < 12 {
		return false
	}
	raw, err := hex.DecodeString(clean)
	if err != nil || len(raw) < 6 {
		return false
	}
	return raw[4] == 0x00 && raw[5] != 0x00
}

// TxWitnessHash returns the wtxid (raw hash bytes, the order the merkle tree
// uses) of a serialized transaction. For legacy transactions the wtxid equals
// the txid.
func TxWitnessHash(txHex string) ([]byte, bool) {
	raw, err := hex.DecodeString(strings.TrimSpace(txHex))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return crypto.Sha256d(raw), true
}

// TxidFromData recomputes the txid (raw hash bytes) of a serialized
// transaction. Witness data is stripped first, because the txid must not cover
// it: this is the piece the merkle branch in getblocktemplate is built from.
func TxidFromData(txHex string) ([]byte, bool) {
	raw, err := hex.DecodeString(strings.TrimSpace(txHex))
	if err != nil || len(raw) < 10 {
		return nil, false
	}

	segwit := raw[4] == 0x00 && raw[5] != 0x00
	bodyStart := 4
	pos := 4
	if segwit {
		bodyStart = 6
		pos = 6
	}

	inCount, pos, ok := readVarInt(raw, pos)
	if !ok {
		return nil, false
	}
	for i := uint64(0); i < inCount; i++ {
		// outpoint (32 hash + 4 index) then varint script length + script + sequence
		if pos+36 > len(raw) {
			return nil, false
		}
		pos += 36
		scriptLen, next, ok := readVarInt(raw, pos)
		if !ok || next > len(raw) {
			return nil, false
		}
		pos = next + int(scriptLen)
		if pos+4 > len(raw) {
			return nil, false
		}
		pos += 4
	}

	outCount, pos, ok := readVarInt(raw, pos)
	if !ok {
		return nil, false
	}
	for i := uint64(0); i < outCount; i++ {
		if pos+8 > len(raw) {
			return nil, false
		}
		pos += 8
		scriptLen, next, ok := readVarInt(raw, pos)
		if !ok || next > len(raw) {
			return nil, false
		}
		pos = next + int(scriptLen)
		if pos > len(raw) {
			return nil, false
		}
	}
	bodyEnd := pos

	if segwit {
		for i := uint64(0); i < inCount; i++ {
			items, next, ok := readVarInt(raw, pos)
			if !ok {
				return nil, false
			}
			pos = next
			for j := uint64(0); j < items; j++ {
				itemLen, afterLen, ok := readVarInt(raw, pos)
				if !ok || afterLen > len(raw) {
					return nil, false
				}
				pos = afterLen + int(itemLen)
				if pos > len(raw) {
					return nil, false
				}
			}
		}
		if pos+4 != len(raw) {
			return nil, false
		}
	}

	stripped := make([]byte, 0, bodyEnd+4)
	stripped = append(stripped, raw[:4]...)
	stripped = append(stripped, raw[bodyStart:bodyEnd]...)
	stripped = append(stripped, raw[len(raw)-4:]...)
	return crypto.Sha256d(stripped), true
}

// WitnessCommitmentScript derives the BIP141 witness commitment scriptPubKey
// for a block whose coinbase is not yet part of the transaction set. The
// result is the complete output script (OP_RETURN + commitment), which the
// coinbase has to carry as a zero-value output whenever the block contains
// witness data.
//
//   - the coinbase wtxid is defined as 32 zero bytes (BIP141)
//   - the witness merkle root is built from the wtxids of the other
//     transactions, in block order
//   - the commitment is SHA256d(witnessRoot || reservedValue)
func WitnessCommitmentScript(txsData []string) string {
	leaves := make([][]byte, 0, len(txsData)+1)
	leaves = append(leaves, witnessReservedValue)

	for _, data := range txsData {
		if wtxid, ok := TxWitnessHash(data); ok {
			leaves = append(leaves, wtxid)
		}
	}

	root := MerkleRoot(leaves)
	commitment := crypto.Sha256d(append(append([]byte{}, root...), witnessReservedValue...))
	return WitnessCommitmentHeader + hex.EncodeToString(commitment)
}

// EffectiveWitnessCommitment decides which witness commitment the coinbase has
// to carry. It returns "" when the block will not carry witness data at all,
// and otherwise always returns the commitment derived from txsData, so the
// coinbase, the witness reserved value and the submitted transactions can
// never disagree.
func EffectiveWitnessCommitment(height int64, templateCommitment string, txsData []string) string {
	txsHaveWitness := false
	for _, data := range txsData {
		if TxHasWitness(data) {
			txsHaveWitness = true
			break
		}
	}

	if !txsHaveWitness && templateCommitment == "" {
		return ""
	}

	if templateCommitment != "" && !IsWitnessCommitmentScript(templateCommitment) {
		log.Printf("[Job] WARNING: template witness commitment at height #%d is not a BIP141 commitment script (%q) - deriving it from the template transactions instead", height, templateCommitment)
	}

	computed := WitnessCommitmentScript(txsData)
	if templateCommitment != "" && !strings.EqualFold(templateCommitment, computed) {
		log.Printf("[Job] Witness commitment mismatch at height #%d: node=%s computed=%s txs=%d witnessTxs=%t - submitting the computed commitment", height, templateCommitment, computed, len(txsData), txsHaveWitness)
	}

	return computed
}

// IsWitnessCommitmentScript reports whether scriptHex is a well formed BIP141
// commitment scriptPubKey (OP_RETURN, 36-byte push, 0xaa21a9ed + 32 bytes).
func IsWitnessCommitmentScript(scriptHex string) bool {
	clean := strings.ToLower(strings.TrimSpace(scriptHex))
	if len(clean) != len(WitnessCommitmentHeader)+64 {
		return false
	}
	return strings.HasPrefix(clean, WitnessCommitmentHeader)
}

// MerkleRoot computes a merkle root over raw (internal order) hashes, using the
// Bitcoin tree layout where an odd node is paired with itself.
func MerkleRoot(hashes [][]byte) []byte {
	if len(hashes) == 0 {
		return make([]byte, 32)
	}

	level := make([][]byte, len(hashes))
	copy(level, hashes)

	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][]byte, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			pair := make([]byte, 0, 64)
			pair = append(pair, level[i]...)
			pair = append(pair, level[i+1]...)
			next = append(next, crypto.Sha256d(pair))
		}
		level = next
	}

	return level[0]
}

// readVarInt decodes a Bitcoin CompactSize integer and returns the new offset.
func readVarInt(b []byte, pos int) (uint64, int, bool) {
	if pos < 0 || pos >= len(b) {
		return 0, pos, false
	}

	switch b[pos] {
	case 0xfd:
		if pos+3 > len(b) {
			return 0, pos, false
		}
		return uint64(binary.LittleEndian.Uint16(b[pos+1:])), pos + 3, true
	case 0xfe:
		if pos+5 > len(b) {
			return 0, pos, false
		}
		return uint64(binary.LittleEndian.Uint32(b[pos+1:])), pos + 5, true
	case 0xff:
		if pos+9 > len(b) {
			return 0, pos, false
		}
		return binary.LittleEndian.Uint64(b[pos+1:]), pos + 9, true
	default:
		return uint64(b[pos]), pos + 1, true
	}
}
