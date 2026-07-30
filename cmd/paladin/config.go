package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppConfig is the installer-written deployment configuration
// (default /etc/paladin/config.json). Every field is optional: absent
// fields fall back to the historical /home/palworld defaults, so a
// dev checkout with no config file behaves exactly as before. Two
// roots derive everything path-shaped:
//
//	install_dir → the game server tree (ini, saves root)
//	data_dir    → Paladin's own state (backups, journal, auth, logs…)
//
// Precedence for the REST admin password:
// env PALWORLD_ADMIN_PASSWORD > admin_password > admin_password_file.
type AppConfig struct {
	Listen            string `json:"listen"`              // host:port for the web UI
	ServerUnit        string `json:"server_unit"`         // systemd unit of the game server
	InstallDir        string `json:"install_dir"`         // e.g. /home/palworld/palserver
	DataDir           string `json:"data_dir"`            // e.g. /home/palworld
	APIURL            string `json:"api_url"`             // Palworld REST base; default local :8212
	AdminPassword     string `json:"admin_password"`      // inline REST password
	AdminPasswordFile string `json:"admin_password_file"` // file containing it (raw, or "AdminPassword: x" line)
	SavCLI            string `json:"sav_cli"`             // sidecar path override
}

const defConfigPath = "/etc/paladin/config.json"

// loadAppConfig reads path (or the default). A missing file is not an
// error — it yields the zero config, i.e. the historical defaults.
func loadAppConfig(path string) (AppConfig, error) {
	var c AppConfig
	if path == "" {
		path = defConfigPath
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// Derived paths, each honoring its root when set.

func (c AppConfig) listen() string {
	if c.Listen != "" {
		return c.Listen
	}
	return defWebAddr
}

func (c AppConfig) serverUnit() string {
	if c.ServerUnit != "" {
		return c.ServerUnit
	}
	return defUnit
}

func (c AppConfig) apiURL() string {
	if c.APIURL != "" {
		return c.APIURL
	}
	return defAPIURL
}

func (c AppConfig) iniPath() string {
	if c.InstallDir != "" {
		return filepath.Join(c.InstallDir, "Pal", "Saved", "Config", "LinuxServer", "PalWorldSettings.ini")
	}
	return defINI
}

func (c AppConfig) savesRoot() string {
	if c.InstallDir != "" {
		return filepath.Join(c.InstallDir, "Pal", "Saved", "SaveGames", "0")
	}
	return defSavesRoot
}

func (c AppConfig) dataPath(name, historical string) string {
	if c.DataDir != "" {
		return filepath.Join(c.DataDir, name)
	}
	return historical
}

func (c AppConfig) backupsDir() string { return c.dataPath("paladin-backups", defBackups) }
func (c AppConfig) journalDir() string { return c.dataPath("paladin-journal", defJournal) }
func (c AppConfig) safetyDir() string  { return c.dataPath("paladin-safety", defSafetyHold) }
func (c AppConfig) authFile() string   { return c.dataPath("paladin-config/auth.json", defAuthFile) }
func (c AppConfig) memRestartFile() string {
	return c.dataPath("paladin-config/memrestart.json", defMemRestart)
}
func (c AppConfig) eventLogFile() string {
	return c.dataPath("paladin-logs/events.jsonl", defEventLog)
}
func (c AppConfig) worldMapFile() string {
	return c.dataPath("paladin-config/worldmap.png", "/home/palworld/paladin-config/worldmap.png")
}

// adminPassword resolves the REST password by precedence. The file form
// accepts either a raw password or the credentials-file format with an
// "AdminPassword: x" line.
func (c AppConfig) adminPassword() (string, error) {
	if pw := os.Getenv("PALWORLD_ADMIN_PASSWORD"); pw != "" {
		return pw, nil
	}
	if c.AdminPassword != "" {
		return c.AdminPassword, nil
	}
	file := c.AdminPasswordFile
	if file == "" {
		file = defCredsFile
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("no admin password: set PALWORLD_ADMIN_PASSWORD, admin_password in config, or make %s readable", file)
	}
	txt := strings.TrimSpace(string(b))
	for _, ln := range strings.Split(txt, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "AdminPassword: "); ok {
			return strings.TrimSpace(v), nil
		}
	}
	if txt != "" && !strings.Contains(txt, "\n") {
		return txt, nil // raw single-line password file
	}
	return "", fmt.Errorf("no admin password found in %s", file)
}
