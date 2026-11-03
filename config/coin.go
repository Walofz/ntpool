package config

import "strings"

// SupportedAlgo is the only proof of work this pool can compute. The block
// header is hashed with it (crypto.Sha256d) and it is the algorithm asked from
// multi-algo nodes, so any other value would produce blocks the pool cannot
// validate locally nor win.
const SupportedAlgo = "sha256d"

// IsSupportedAlgo reports whether this build can mine with algo.
func IsSupportedAlgo(algo string) bool {
	return strings.EqualFold(strings.TrimSpace(algo), SupportedAlgo)
}

// CoinProfile collects everything the pool has to do differently for one chain.
// The pool itself stays coin agnostic: switching coins is one environment
// variable (COIN), and the parts that cannot be hardcoded (the mining algorithm
// the node is currently handing out, extra rules it demands) are read from the
// node at runtime.
type CoinProfile struct {
	// Name is the COIN value that selects this profile.
	Name string
	// Symbol is used for display and notifications when COIN_SYMBOL is unset.
	Symbol string
	// Algo is the proof of work hash this pool implementation can compute.
	// The template is requested for this algorithm when the node supports it,
	// and a template for a different algorithm is reported as unusable.
	Algo string
	// GbtRules are the BIP9 rules the client claims to understand when it asks
	// for a block template.
	GbtRules []string
	// GbtAlgoParam reports that getblocktemplate accepts the mining algorithm
	// as a second positional parameter. Both DigiByte and Auroracoin are
	// multi-algo chains with this signature; without it the node builds the
	// template for its own startup `miningalgo` default, which may not be the
	// algorithm this pool hashes with.
	GbtAlgoParam bool
	// GbtAlgos lists the algorithm names the node accepts when GbtAlgoParam is
	// set, so a typo in RPC_ALGO can be reported at startup instead of showing
	// up as a broken template.
	GbtAlgos []string
	// WitnessData reports whether blocks on this chain may carry witness data,
	// i.e. whether the coinbase may hold the 32-byte reserved value. Auroracoin
	// returns a default_witness_commitment in its template but rejects any block
	// containing witness data with "unexpected-witness", so the reserved value
	// has to stay out there; the commitment output itself is harmless.
	WitnessData bool
}

var coinProfiles = map[string]CoinProfile{
	"aur": {
		Name:         "aur",
		Symbol:       "AUR",
		Algo:         "sha256d",
		GbtRules:     []string{"segwit"},
		GbtAlgoParam: true,
		GbtAlgos:     []string{"sha256d", "scrypt", "groestl", "skein", "qubit"},
		// The node publishes a witness commitment but its validation still
		// rejects witness data, so the reserved value must not be added.
		WitnessData: false,
	},
	"dgb": {
		Name:         "dgb",
		Symbol:       "DGB",
		Algo:         "sha256d",
		GbtRules:     []string{"segwit"},
		GbtAlgoParam: true,
		GbtAlgos:     []string{"sha256d", "scrypt", "groestl", "skein", "qubit", "odo"},
		WitnessData:  true,
	},
	"btc": {
		Name:        "btc",
		Symbol:      "BTC",
		Algo:        "sha256d",
		GbtRules:    []string{"segwit"},
		GbtAlgos:    []string{"sha256d"},
		WitnessData: true,
	},
}

// CoinProfileNames lists the profiles that can be selected with COIN.
func CoinProfileNames() []string {
	names := make([]string, 0, len(coinProfiles))
	for name := range coinProfiles {
		names = append(names, name)
	}
	return names
}

// ResolveCoinProfile returns the profile selected by key (the COIN variable).
// When key is empty or unknown it falls back to symbol (COIN_SYMBOL) and finally
// to a generic segwit profile, so builds that only set COIN_SYMBOL keep the
// behaviour of earlier releases.
func ResolveCoinProfile(key, symbol string) CoinProfile {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(symbol))
	}
	if profile, ok := coinProfiles[key]; ok {
		return profile
	}

	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		symbol = "BTC"
	}
	return CoinProfile{
		Name:        "generic",
		Symbol:      symbol,
		Algo:        "sha256d",
		GbtRules:    []string{"segwit"},
		WitnessData: true,
	}
}

// UsesSegwit reports whether the profile asks the node for segwit templates.
func (p CoinProfile) UsesSegwit() bool {
	for _, rule := range p.GbtRules {
		if rule == "segwit" {
			return true
		}
	}
	return false
}

// SupportsAlgo reports whether algo is one of the algorithm names the profile's
// node accepts. A profile without a known algorithm list accepts anything.
func (p CoinProfile) SupportsAlgo(algo string) bool {
	if len(p.GbtAlgos) == 0 {
		return true
	}
	for _, known := range p.GbtAlgos {
		if strings.EqualFold(known, algo) {
			return true
		}
	}
	return false
}
