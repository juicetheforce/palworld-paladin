package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

const sampleINI = `[/Script/Pal.PalGameWorldSettings]
OptionSettings=(Difficulty=None,DayTimeSpeedRate=1.000000,ExpRate=1.000000,ServerName="A, B=C",CrossplayPlatforms=(Steam,Xbox,PS5,Mac),PlayerStomachDecreaceRate=1.000000,bEnableVoiceChat=False,AutoSaveSpan=30.000000,AdminPassword="secret",RESTAPIEnabled=True)
`

// ---- key list (the REAL embedded artifact) ---------------------------------

func TestEmbeddedKeyListLoads(t *testing.T) {
	kl, err := LoadKeyList()
	if err != nil {
		t.Fatalf("LoadKeyList: %v", err)
	}
	if len(kl.Keys) != 120 {
		t.Fatalf("expected the verified 120-key artifact, got %d", len(kl.Keys))
	}
	// Case-insensitive lookup (the autoSaveSpan lesson).
	d, ok := kl.Lookup("autosavespan")
	if !ok || d.Key != "AutoSaveSpan" {
		t.Fatalf("case-insensitive lookup failed: %+v ok=%v", d, ok)
	}
	// Password keys are readback-invisible.
	ap, _ := kl.Lookup("ServerPassword")
	if ap.ReadbackVerifiable() {
		t.Fatal("AdminPassword must be rest_readback:false")
	}
	er, _ := kl.Lookup("ExpRate")
	if !er.ReadbackVerifiable() {
		t.Fatal("ordinary keys must be readback-verifiable")
	}
}

func TestValidateStaged(t *testing.T) {
	kl, _ := LoadKeyList()
	cases := []struct {
		name string
		diff map[string]any
		want string // substring of the error; "" = valid
	}{
		{"valid mix", map[string]any{
			"ExpRate": 2.0, "bEnableVoiceChat": true, "DeathPenalty": "Item",
			"ServerName": "My Server", "CrossplayPlatforms": "(Steam,Mac)",
			"ServerPlayerMaxNum": 16.0,
		}, ""},
		{"unknown key", map[string]any{"ExpRateX": 2.0}, "unknown key"},
		{"wrong casing rejected for writes", map[string]any{"playerstomachdecreacerate": 0.5}, "wrong casing"},
		{"deprecated blocked", map[string]any{"AllowConnectPlatform": "Steam"}, "deprecated"},
		{"bool type", map[string]any{"bEnableVoiceChat": "True"}, "want bool"},
		{"int rejects fraction", map[string]any{"ServerPlayerMaxNum": 16.5}, "want integer"},
		{"range max", map[string]any{"ServerPlayerMaxNum": 64.0}, "above maximum"},
		{"enum member", map[string]any{"DeathPenalty": "Everything"}, "not one of"},
		{"string quote corruption", map[string]any{"ServerName": `has "quote"`}, "may not contain quotes"},
		{"list shape", map[string]any{"CrossplayPlatforms": "Steam,Mac"}, "raw tuples"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := kl.ValidateStaged(tc.diff)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// ---- ini parser ------------------------------------------------------------

func TestINIRoundTripIsLossless(t *testing.T) {
	ini, err := ParseINI(sampleINI)
	if err != nil {
		t.Fatalf("ParseINI: %v", err)
	}
	if got := ini.Serialize(); got != sampleINI {
		t.Fatalf("round trip not lossless:\n got: %q\nwant: %q", got, sampleINI)
	}
	// Quoted commas/equals and nested tuples must parse as single values.
	if v, _ := ini.Get("ServerName"); v != `"A, B=C"` {
		t.Fatalf("ServerName parsed wrong: %q", v)
	}
	if v, _ := ini.Get("CrossplayPlatforms"); v != "(Steam,Xbox,PS5,Mac)" {
		t.Fatalf("CrossplayPlatforms parsed wrong: %q", v)
	}
}

func TestINIStructuralValidation(t *testing.T) {
	bad := []struct{ name, content, want string }{
		{"two option lines",
			"[/Script/Pal.PalGameWorldSettings]\nOptionSettings=(A=1)\nOptionSettings=(B=2)\n",
			"exactly one OptionSettings"},
		{"stray line (the classic silent reset)",
			"[/Script/Pal.PalGameWorldSettings]\nOptionSettings=(A=1)\nExpRate=2\n",
			"silently resets"},
		{"unbalanced quote",
			"[/Script/Pal.PalGameWorldSettings]\nOptionSettings=(ServerName=\"oops)\n",
			"unbalanced"},
		{"missing header", "OptionSettings=(A=1)\n", "missing section header"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseINI(tc.content); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestFormatValueMatchesGameSerialization(t *testing.T) {
	kl, _ := LoadKeyList()
	f := func(key string, v any) string {
		d, _ := kl.Lookup(key)
		s, err := FormatValue(d, v)
		if err != nil {
			t.Fatalf("FormatValue(%s): %v", key, err)
		}
		return s
	}
	if got := f("ExpRate", 2.0); got != "2.000000" {
		t.Fatalf("float: %q", got)
	}
	if got := f("bEnableVoiceChat", true); got != "True" {
		t.Fatalf("bool: %q", got)
	}
	if got := f("ServerPlayerMaxNum", 16.0); got != "16" {
		t.Fatalf("int: %q", got)
	}
	if got := f("ServerName", "Hi"); got != `"Hi"` {
		t.Fatalf("string: %q", got)
	}
	if got := f("DeathPenalty", "Item"); got != "Item" {
		t.Fatalf("enum: %q", got)
	}
}

// ---- payload: apply / rollback / verify ------------------------------------

func newPayload(t *testing.T) (*CommitPayload, string) {
	t.Helper()
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "PalWorldSettings.ini")
	if err := os.WriteFile(iniPath, []byte(sampleINI), 0o640); err != nil {
		t.Fatal(err)
	}
	kl, _ := LoadKeyList()
	return &CommitPayload{
		KeyList: kl, INIPath: iniPath, WorldDir: dir,
		Staged: map[string]any{
			"ExpRate": 2.0, "bEnableVoiceChat": true, "AutoSaveSpan": 60.0,
			"ServerPassword": "newpw",
		},
		WorldBackup:  func(context.Context) error { return nil },
		ReadSettings: func(context.Context) (map[string]any, error) { return nil, nil },
		BackupAnchor: "/backups/test",
	}, iniPath
}

func TestApplyWritesAndRollbackRestores(t *testing.T) {
	p, iniPath := newPayload(t)
	if err := p.PreCheck(context.Background()); err != nil {
		t.Fatalf("PreCheck: %v", err)
	}
	if err := p.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	b, _ := os.ReadFile(iniPath)
	content := string(b)
	for _, want := range []string{"ExpRate=2.000000", "bEnableVoiceChat=True",
		"AutoSaveSpan=60.000000", `ServerPassword="newpw"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("applied ini missing %q:\n%s", want, content)
		}
	}
	// Untouched keys survive byte-for-byte.
	if !strings.Contains(content, `ServerName="A, B=C"`) {
		t.Fatal("untouched key was disturbed")
	}
	// Pre-write copy exists and matches the original.
	prev, err := os.ReadFile(iniPath + ".paladin-prev")
	if err != nil || string(prev) != sampleINI {
		t.Fatalf("pre-write copy wrong: %v", err)
	}
	// Rollback restores the original exactly.
	if err := p.RollbackApply(context.Background()); err != nil {
		t.Fatalf("RollbackApply: %v", err)
	}
	after, _ := os.ReadFile(iniPath)
	if string(after) != sampleINI {
		t.Fatal("rollback did not restore original bytes")
	}
}

func TestVerifyHonesty(t *testing.T) {
	p, _ := newPayload(t)
	if err := p.PreCheck(context.Background()); err != nil {
		t.Fatalf("PreCheck: %v", err)
	}
	// Readback: camelCased AutoSaveSpan (the live quirk), correct ExpRate,
	// WRONG voice-chat value, and no password echo.
	p.ReadSettings = func(context.Context) (map[string]any, error) {
		return map[string]any{
			"autoSaveSpan": 60.0, "ExpRate": 2.0, "bEnableVoiceChat": false,
		}, nil
	}
	res, err := p.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	warns := strings.Join(res.Warnings, "\n")
	notes := strings.Join(res.Notes, "\n")
	if strings.Contains(warns, "AutoSaveSpan: readback shows") {
		t.Fatalf("case-insensitive match failed — false 'didn't apply' on AutoSaveSpan:\n%s", warns)
	}
	// A real mismatch is a WARNING.
	if !strings.Contains(warns, "bEnableVoiceChat: readback shows false") {
		t.Fatalf("real mismatch must be a warning:\n%s", warns)
	}
	// Not-verifiable password is a NOTE, not a warning.
	if !strings.Contains(notes, "ServerPassword: applied — not verifiable") {
		t.Fatalf("rest_readback:false key must be an informational note:\n%s", notes)
	}
	if strings.Contains(warns, "ServerPassword") {
		t.Fatalf("not-verifiable password must NOT be a warning:\n%s", warns)
	}
	// Gotcha context is a NOTE.
	if !strings.Contains(notes, "note") {
		t.Fatalf("gotcha context must be an informational note:\n%s", notes)
	}
}

func TestWorldOptionSavWarningSurfaces(t *testing.T) {
	p, _ := newPayload(t)
	if err := os.WriteFile(filepath.Join(p.WorldDir, "WorldOption.sav"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := p.PreCheck(context.Background()); err != nil {
		t.Fatalf("PreCheck: %v", err)
	}
	p.ReadSettings = func(context.Context) (map[string]any, error) {
		return map[string]any{"ExpRate": 2.0, "autoSaveSpan": 60.0, "bEnableVoiceChat": true}, nil
	}
	res, _ := p.Verify(context.Background())
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "WorldOption.sav exists") {
		t.Fatalf("WorldOption.sav must surface as a warning: %v", res.Warnings)
	}
}

// ---- wiring: the real payload through the real engine ----------------------

type okAPI struct{}

func (okAPI) Announce(context.Context, string) error         { return nil }
func (okAPI) Save(context.Context) error                     { return nil }
func (okAPI) WaitReady(context.Context, time.Duration) error { return nil }

type okUnit struct{}

func (okUnit) Start(context.Context) error                      { return nil }
func (okUnit) Stop(context.Context) error                       { return nil }
func (okUnit) Kill(context.Context) error                       { return nil }
func (okUnit) WaitStopped(context.Context, time.Duration) error { return nil }

type okSusp struct{}

func (okSusp) Suspend() {}
func (okSusp) Resume()  {}

func TestCommitThroughRealEngine(t *testing.T) {
	p, iniPath := newPayload(t)
	p.ReadSettings = func(context.Context) (map[string]any, error) {
		return map[string]any{"ExpRate": 2.0, "autoSaveSpan": 60.0, "bEnableVoiceChat": true}, nil
	}
	j, err := maintain.NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := maintain.NewEngine(maintain.Config{
		API: okAPI{}, Unit: okUnit{}, Susp: okSusp{}, Journal: j,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := eng.Run(context.Background(), "wire-1", p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// AdminPassword staged → not-verifiable NOTE; ExpRate/voice have no
	// mismatch. With only notes (no warnings), this is clean SUCCESS now.
	if out.Status != maintain.StatusSuccess {
		t.Fatalf("want success (notes only, no warnings), got %+v", out)
	}
	if len(out.VerifyNotes) == 0 {
		t.Fatal("expected informational notes (password not-verifiable)")
	}
	b, _ := os.ReadFile(iniPath)
	if !strings.Contains(string(b), "ExpRate=2.000000") {
		t.Fatal("engine-driven commit did not land on disk")
	}
}

func TestValidateStagedRejectsProtectedKeys(t *testing.T) {
	kl, err := LoadKeyList()
	if err != nil {
		t.Fatal(err)
	}
	err = kl.ValidateStaged(map[string]any{"AdminPassword": "newpass"})
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("protected key must be rejected with explanation: %v", err)
	}
	if err := kl.ValidateStaged(map[string]any{"ExpRate": 2.0}); err != nil {
		t.Fatalf("normal key must pass: %v", err)
	}
}
