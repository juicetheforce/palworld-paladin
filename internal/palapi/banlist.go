package palapi

import (
	"bufio"
	"os"
	"strings"
)

// BanList reads the server's banlist.txt (Pal/Saved/SaveGames/banlist.txt).
// The REST API bans/unbans but does not expose the list for reading, so
// the moderation UI reads the file directly (verified format:
// "steam_<id>,<opaque-32-hex>" per line; DESIGN.md §6.7).
//
// This is a plain file read — no game connection needed — so the ban list
// is visible even while the server is stopped.

// BanEntry is one banned identity.
type BanEntry struct {
	UserID string `json:"user_id"` // steam_<id> — the moderation identifier
	Raw    string `json:"raw"`     // the full line, for transparency
}

// ReadBanList parses banlist.txt at the given path. A missing file is an
// empty list (not an error) — an unbanned server simply has no file.
func ReadBanList(path string) ([]BanEntry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []BanEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		id := line
		if comma := strings.IndexByte(line, ','); comma >= 0 {
			id = line[:comma]
		}
		out = append(out, BanEntry{UserID: id, Raw: line})
	}
	return out, sc.Err()
}
