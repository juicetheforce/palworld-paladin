package palapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBanList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.txt")

	// Missing file → empty list, no error.
	entries, err := ReadBanList(path)
	if err != nil || entries != nil {
		t.Fatalf("missing file should be empty: %v %v", entries, err)
	}

	os.WriteFile(path, []byte("steam_76561198000000001,abc123def456\nsteam_76561198000000002,999888777\n\n"), 0o644)
	entries, err = ReadBanList(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].UserID != "steam_76561198000000001" {
		t.Fatalf("bad parse: %+v", entries[0])
	}
	// Blank lines ignored.
}
