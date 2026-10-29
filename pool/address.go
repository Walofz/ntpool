package pool

import (
	"bytes"
	"encoding/hex"
	"strings"
)

// burnScriptPubKeyHex is a P2PKH output paying to hash160 = 0. It is emitted
// when an address cannot be decoded: the block stays consensus-valid but the
// coins are unspendable, so callers should validate addresses beforehand.
const burnScriptPubKeyHex = "76a914000000000000000000000000000000000000000088ac"

func burnScriptPubKey() []byte {
	script, _ := hex.DecodeString(burnScriptPubKeyHex)
	return script
}

// IsValidAddress reports whether address decodes into a spendable scriptPubKey.
// It reuses AddressToScriptPubKey, so a true result guarantees the coinbase
// output is not the burn fallback.
func IsValidAddress(address string) bool {
	if strings.TrimSpace(address) == "" {
		return false
	}
	return !bytes.Equal(AddressToScriptPubKey(address), burnScriptPubKey())
}
