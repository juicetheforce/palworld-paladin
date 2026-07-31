// Package steam wraps the SteamCMD interactions the update cycle needs:
// finding the steamcmd binary, comparing the installed buildid against
// Steam's public branch (so Paladin can tell whether an update exists
// WITHOUT stopping the server), and running the actual app update with
// live output streaming (DESIGN.md §6.4 Update).
package steam

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// PalworldAppID is the Palworld dedicated server's Steam app id.
const PalworldAppID = "2394010"

// FindSteamCMD locates the steamcmd binary. Order: the PALADIN_STEAMCMD
// env override, PATH, then the common install locations (apt's
// /usr/games, the user-local tarball spots).
func FindSteamCMD() (string, error) {
	if p := os.Getenv("PALADIN_STEAMCMD"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("steam: PALADIN_STEAMCMD=%q does not exist", p)
	}
	if p, err := exec.LookPath("steamcmd"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, cand := range []string{
		"/usr/games/steamcmd",
		filepath.Join(home, ".steam", "steamcmd", "steamcmd.sh"),
		filepath.Join(home, "steamcmd", "steamcmd.sh"),
		filepath.Join(home, "Steam", "steamcmd.sh"),
	} {
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("steam: steamcmd not found (set PALADIN_STEAMCMD to its path)")
}

var buildidRe = regexp.MustCompile(`"buildid"\s*"(\d+)"`)

// LocalBuildID reads the installed buildid from the app manifest under the
// install dir (steamapps/appmanifest_<app>.acf). This is the ground truth
// for "what version is on disk" and needs no network or steamcmd run.
func LocalBuildID(installDir, appID string) (string, error) {
	// Two layouts exist in the wild:
	//  - force_install_dir installs: <installDir>/steamapps/appmanifest_*.acf
	//  - steamapps/common installs (installDir IS .../steamapps/common/<App>):
	//    the manifest lives two levels up, at .../steamapps/appmanifest_*.acf
	name := "appmanifest_" + appID + ".acf"
	cands := []string{
		filepath.Join(installDir, "steamapps", name),
		filepath.Clean(filepath.Join(installDir, "..", "..", name)),
	}
	var lastErr error
	for _, m := range cands {
		b, err := os.ReadFile(m)
		if err == nil {
			return parseManifestBuildID(string(b))
		}
		lastErr = err
	}
	return "", fmt.Errorf("steam: no app manifest found (tried %s and %s): %w", cands[0], cands[1], lastErr)
}

func parseManifestBuildID(acf string) (string, error) {
	m := buildidRe.FindStringSubmatch(acf)
	if m == nil {
		return "", fmt.Errorf("steam: no buildid in manifest")
	}
	return m[1], nil
}

// RemoteBuildID asks Steam for the public branch's current buildid via
// steamcmd app_info_print. +app_info_update 1 forces fresh (non-cached)
// app info. Requires network; takes tens of seconds on a cold steamcmd.
func RemoteBuildID(ctx context.Context, steamcmd, appID string) (string, error) {
	cmd := exec.CommandContext(ctx, steamcmd,
		"+login", "anonymous",
		"+app_info_update", "1",
		"+app_info_print", appID,
		"+quit")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("steam: app_info_print: %w (output tail: %s)", err, tail(string(out), 300))
	}
	return parseAppInfoPublicBuildID(string(out))
}

// parseAppInfoPublicBuildID extracts branches→public→buildid from
// steamcmd's VDF-ish app_info_print output.
func parseAppInfoPublicBuildID(out string) (string, error) {
	branches := strings.Index(out, `"branches"`)
	if branches < 0 {
		return "", fmt.Errorf("steam: no branches section in app info")
	}
	pub := strings.Index(out[branches:], `"public"`)
	if pub < 0 {
		return "", fmt.Errorf("steam: no public branch in app info")
	}
	m := buildidRe.FindStringSubmatch(out[branches+pub:])
	if m == nil {
		return "", fmt.Errorf("steam: no buildid under public branch")
	}
	return m[1], nil
}

// RunUpdate performs the actual app update into installDir, streaming each
// output line to onLine as it happens (feeds the live viewer). Success is
// judged by steamcmd's own success strings, not just the exit code (which
// steamcmd has historically been sloppy about).
func RunUpdate(ctx context.Context, steamcmd, installDir, appID string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, steamcmd,
		"+force_install_dir", installDir,
		"+login", "anonymous",
		"+app_update", appID,
		"+quit")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // interleave

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("steam: start steamcmd: %w", err)
	}

	var sawSuccess, sawUpToDate bool
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if line == "" {
			continue
		}
		if onLine != nil {
			onLine(line)
		}
		if strings.Contains(line, "Success! App '"+appID+"' fully installed") {
			sawSuccess = true
		}
		if strings.Contains(line, "already up to date") {
			sawUpToDate = true
		}
	}
	werr := cmd.Wait()
	if sawSuccess || sawUpToDate {
		return nil
	}
	if werr != nil {
		return fmt.Errorf("steam: update failed: %w", werr)
	}
	return fmt.Errorf("steam: update finished without a success confirmation")
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
