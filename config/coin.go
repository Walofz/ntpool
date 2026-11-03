package config

import "strings"

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
	// as a second positional parameter, as DigiByte multi-algo nodes do.
	GbtAlgoParam bool
}

var coinProfiles = map[string]CoinProfile{
	"aur": {
		Name:     "aur",
		Symbol:   "AUR",
		Algo:     "sha256d",
		GbtRules: []string{"segwit"},
	},
	"dgb": {
		Name:         "dgb",
		Symbol:       "DGB",
		Algo:         "sha256d",
		GbtRules:     []string{"segwit"},
		GbtAlgoParam: true,
	},
	"btc": {
		Name:     "btc",
		Symbol:   "BTC",
		Algo:     "sha256d",
		GbtRules: []string{"segwit"},
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
		Name:     "generic",
		Symbol:   symbol,
		Algo:     "sha256d",
		GbtRules: []string{"segwit"},
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
