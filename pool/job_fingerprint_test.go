package pool

import "testing"

func fingerprintTemplate(height float64, prevHash string, coinbaseValue float64, curtime float64, txids ...string) map[string]interface{} {
	txs := make([]interface{}, 0, len(txids))
	for _, id := range txids {
		txs = append(txs, map[string]interface{}{"txid": id, "data": "01000000" + id})
	}
	return map[string]interface{}{
		"height":            height,
		"previousblockhash": prevHash,
		"coinbasevalue":     coinbaseValue,
		"version":           536870912.0,
		"bits":              "1d00ffff",
		"curtime":           curtime,
		"target":            "00000000ffff0000000000000000000000000000000000000000000000000000",
		"transactions":      txs,
	}
}

func TestTemplateFingerprintIgnoresVolatileFields(t *testing.T) {
	first := fingerprintTemplate(1000, "aa11", 625000000.0, 1700000000, "tx1", "tx2")
	second := fingerprintTemplate(1000, "aa11", 625000000.0, 1700000003, "tx1", "tx2")

	if a, b := TemplateFingerprint(first), TemplateFingerprint(second); a != b {
		t.Fatalf("curtime must not change the fingerprint: %s != %s", a, b)
	}

	// A template without any transactions must stay stable as well.
	empty := TemplateFingerprint(map[string]interface{}{"height": 1.0})
	if empty == "" {
		t.Fatal("fingerprint must not be empty for a populated template")
	}
	if TemplateFingerprint(nil) != "" {
		t.Fatal("nil template must produce an empty fingerprint")
	}
}

func TestTemplateFingerprintDetectsChanges(t *testing.T) {
	base := TemplateFingerprint(fingerprintTemplate(1000, "aa11", 625000000.0, 1700000000, "tx1", "tx2"))

	cases := map[string]map[string]interface{}{
		"height":         fingerprintTemplate(1001, "aa11", 625000000.0, 1700000000, "tx1", "tx2"),
		"previous hash":  fingerprintTemplate(1000, "bb22", 625000000.0, 1700000000, "tx1", "tx2"),
		"coinbase value": fingerprintTemplate(1000, "aa11", 625010000.0, 1700000000, "tx1", "tx2"),
		"added tx":       fingerprintTemplate(1000, "aa11", 625000000.0, 1700000000, "tx1", "tx2", "tx3"),
		"reordered txs":  fingerprintTemplate(1000, "aa11", 625000000.0, 1700000000, "tx2", "tx1"),
	}

	for name, tmpl := range cases {
		if got := TemplateFingerprint(tmpl); got == base {
			t.Fatalf("%s change must alter the fingerprint (got the same %s)", name, got)
		}
	}
}
