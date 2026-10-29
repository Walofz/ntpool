package pool

import (
	"encoding/hex"
	"testing"
)

// The wallet used in production must keep decoding to the same scriptPubKey.
// If this changes, every coinbase output changes and rewards may be burned.
func TestAddressToScriptPubKeyGoldenWallet(t *testing.T) {
	const want = "76a914a0620c004ed85718b7653b48543e8ab725b3f2a488ac"

	got := hex.EncodeToString(AddressToScriptPubKey("AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr"))
	if got != want {
		t.Fatalf("coinbase output script changed: got %s, want %s", got, want)
	}
}

func TestAddressToScriptPubKeyFallback(t *testing.T) {
	for _, addr := range []string{"", "   ", "not-an-address", "!!!"} {
		got := hex.EncodeToString(AddressToScriptPubKey(addr))
		if got != burnScriptPubKeyHex {
			t.Fatalf("address %q: expected burn fallback %s, got %s", addr, burnScriptPubKeyHex, got)
		}
	}
}

func TestIsValidAddress(t *testing.T) {
	tests := []struct {
		address string
		valid   bool
	}{
		{"AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr", true},
		{"76a914a0620c004ed85718b7653b48543e8ab725b3f2a488ac", true},
		{"", false},
		{"   ", false},
		{"not-an-address", false},
		{"1BoatSLRHtKNngkdXEeobR76b53LETtpyT", true},
	}

	for _, tt := range tests {
		if got := IsValidAddress(tt.address); got != tt.valid {
			t.Fatalf("IsValidAddress(%q) = %v, want %v", tt.address, got, tt.valid)
		}
	}
}

// A payment script identical to the burn fallback must never be reported valid,
// even though it is the correct encoding for that hash160.
func TestIsValidAddressRejectsBurnScript(t *testing.T) {
	if IsValidAddress(burnScriptPubKeyHex) {
		t.Fatal("burn fallback script must not be treated as a valid payout address")
	}
}
