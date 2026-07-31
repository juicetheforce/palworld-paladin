package settings

import (
	"strings"
	"testing"
)

func TestParseINIEscapedQuotesInValues(t *testing.T) {
	// UE writes \" inside quoted strings — a server name/description with a
	// quote character must not desynchronize the tokenizer (live-box bug).
	ini := `[/Script/Pal.PalGameWorldSettings]
OptionSettings=(ServerName="Ryan\"s \"Cool\" Server",ServerDescription="Say \"hi\", have fun (really)",ExpRate=1.000000)`
	p, err := ParseINI(ini)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := p.Get("ServerName")
	if !ok || v != `"Ryan\"s \"Cool\" Server"` {
		t.Fatalf("escaped-quote value mangled: %q", v)
	}
	if v, _ := p.Get("ExpRate"); v != "1.000000" {
		t.Fatalf("keys after the escaped value must survive: %q", v)
	}
}

func TestParseINIUnbalancedNamesThePosition(t *testing.T) {
	// The live-box corruption: a rival tool comma-split the crossplay
	// tuple and quoted a fragment. The error must point AT it.
	ini := `[/Script/Pal.PalGameWorldSettings]
OptionSettings=(A=1,CrossplayPlatforms="(Steam",Xbox=,Mac)=,B=2)`
	_, err := ParseINI(ini)
	if err == nil {
		t.Fatal("mangled tuple must error")
	}
	if !strings.Contains(err.Error(), "near character") || !strings.Contains(err.Error(), "Mac)") {
		t.Fatalf("error must locate the imbalance: %v", err)
	}
}
