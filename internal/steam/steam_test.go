package steam

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleManifest = `"AppState"
{
	"appid"		"2394010"
	"Universe"		"1"
	"name"		"PalServer"
	"StateFlags"		"4"
	"installdir"		"PalServer"
	"LastUpdated"		"1721000000"
	"SizeOnDisk"		"12345678901"
	"StagingSize"		"0"
	"buildid"		"19026848"
	"LastOwner"		"76561190000000000"
}`

// Realistic slice of app_info_print output: note the depot sections also
// contain "buildid"-like keys in other branches; the parser must pick the
// PUBLIC branch's.
const sampleAppInfo = `"2394010"
{
	"common"
	{
		"name"		"Palworld Dedicated Server"
	}
	"depots"
	{
		"branches"
		{
			"public"
			{
				"buildid"		"19123456"
				"timeupdated"		"1722200000"
			}
			"experimental"
			{
				"buildid"		"19999999"
				"timeupdated"		"1722200001"
			}
		}
	}
}`

func TestParseManifestBuildID(t *testing.T) {
	id, err := parseManifestBuildID(sampleManifest)
	if err != nil || id != "19026848" {
		t.Fatalf("want 19026848, got %q err=%v", id, err)
	}
	if _, err := parseManifestBuildID("no buildid here"); err == nil {
		t.Fatal("missing buildid must error")
	}
}

func TestParseAppInfoPublicBuildID(t *testing.T) {
	id, err := parseAppInfoPublicBuildID(sampleAppInfo)
	if err != nil || id != "19123456" {
		t.Fatalf("want public 19123456 (not experimental), got %q err=%v", id, err)
	}
	if _, err := parseAppInfoPublicBuildID("garbage"); err == nil {
		t.Fatal("missing branches must error")
	}
}

func TestLocalBuildIDReadsManifest(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "steamapps"), 0o755)
	os.WriteFile(filepath.Join(dir, "steamapps", "appmanifest_2394010.acf"), []byte(sampleManifest), 0o644)
	id, err := LocalBuildID(dir, "2394010")
	if err != nil || id != "19026848" {
		t.Fatalf("LocalBuildID: %q %v", id, err)
	}
	if _, err := LocalBuildID(t.TempDir(), "2394010"); err == nil {
		t.Fatal("missing manifest must error")
	}
}
