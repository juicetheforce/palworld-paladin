package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaultsWhenAbsent(t *testing.T) {
	c, err := loadAppConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal("missing config must not error:", err)
	}
	if c.iniPath() != defINI || c.serverUnit() != defUnit || c.backupsDir() != defBackups {
		t.Fatal("zero config must yield historical defaults")
	}
	if c.listen() != defWebAddr {
		t.Fatal("listen default")
	}
}

func TestConfigDerivations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{
	  "listen": "0.0.0.0:9090",
	  "server_unit": "mypal.service",
	  "install_dir": "/srv/pal/server",
	  "data_dir": "/srv/pal"
	}`), 0o644)
	c, err := loadAppConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.iniPath() != "/srv/pal/server/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini" {
		t.Fatal("ini derivation:", c.iniPath())
	}
	if c.savesRoot() != "/srv/pal/server/Pal/Saved/SaveGames/0" {
		t.Fatal("saves derivation")
	}
	if c.backupsDir() != "/srv/pal/paladin-backups" || c.authFile() != "/srv/pal/paladin-config/auth.json" {
		t.Fatal("data derivations")
	}
	if c.serverUnit() != "mypal.service" || c.listen() != "0.0.0.0:9090" {
		t.Fatal("direct fields")
	}
}

func TestAdminPasswordPrecedence(t *testing.T) {
	dir := t.TempDir()
	credsFile := filepath.Join(dir, "creds.txt")
	os.WriteFile(credsFile, []byte("ServerName: x\nAdminPassword: from-file\n"), 0o644)

	// file form (credentials format)
	c := AppConfig{AdminPasswordFile: credsFile}
	if pw, _ := c.adminPassword(); pw != "from-file" {
		t.Fatal("credentials-format file:", pw)
	}
	// raw single-line file form
	raw := filepath.Join(dir, "raw.txt")
	os.WriteFile(raw, []byte("just-a-password\n"), 0o644)
	c = AppConfig{AdminPasswordFile: raw}
	if pw, _ := c.adminPassword(); pw != "just-a-password" {
		t.Fatal("raw file:", pw)
	}
	// inline beats file
	c = AppConfig{AdminPassword: "inline", AdminPasswordFile: credsFile}
	if pw, _ := c.adminPassword(); pw != "inline" {
		t.Fatal("inline precedence")
	}
	// env beats everything
	t.Setenv("PALWORLD_ADMIN_PASSWORD", "from-env")
	if pw, _ := c.adminPassword(); pw != "from-env" {
		t.Fatal("env precedence")
	}
}
