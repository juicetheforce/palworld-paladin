package settings

import "testing"

func TestParseValue(t *testing.T) {
	kl, _ := LoadKeyList()
	def := func(k string) *KeyDef { d, _ := kl.Lookup(k); return d }

	if v, err := ParseValue(def("bEnableVoiceChat"), "true"); err != nil || v != true {
		t.Fatalf("bool: %v %v", v, err)
	}
	if _, err := ParseValue(def("bEnableVoiceChat"), "maybe"); err == nil {
		t.Fatal("bad bool must error")
	}
	if v, err := ParseValue(def("ExpRate"), "2.5"); err != nil || v != 2.5 {
		t.Fatalf("float: %v %v", v, err)
	}
	if v, err := ParseValue(def("ServerPlayerMaxNum"), "16"); err != nil || v != 16.0 {
		t.Fatalf("int-as-number: %v %v", v, err)
	}
	if _, err := ParseValue(def("ExpRate"), "lots"); err == nil {
		t.Fatal("bad number must error")
	}
	if v, err := ParseValue(def("DeathPenalty"), "Item"); err != nil || v != "Item" {
		t.Fatalf("enum passthrough: %v %v", v, err)
	}
	// Parse permits, validate rejects — division of labor.
	v, _ := ParseValue(def("DeathPenalty"), "Everything")
	if err := kl.ValidateStaged(map[string]any{"DeathPenalty": v}); err == nil {
		t.Fatal("invalid enum must be caught by ValidateStaged")
	}
}
