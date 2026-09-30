package main

import (
	"testing"
	"time"
)

// deploy-test.sh stamps test builds "dev-<commit>[-dirty]". Those must be
// treated exactly like plain "dev" builds: no update indicator, because
// there's no release to compare a dev build against.
func TestIsDevBuild(t *testing.T) {
	for v, want := range map[string]bool{
		"":                  true,
		"dev":               true,
		"dev-abc1234":       true,
		"dev-abc1234-dirty": true,
		"v0.3.0":            false,
		"development":       false, // only "dev" or the "dev-" prefix
	} {
		if got := isDevBuild(v); got != want {
			t.Errorf("isDevBuild(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestUpdateIndicatorSuppressedOnDevStamps(t *testing.T) {
	for _, v := range []string{"dev", "dev-abc1234", "dev-abc1234-dirty"} {
		if cachedPaladinLatest(v, time.Hour) != nil {
			t.Errorf("%q: update check must be disabled for dev builds", v)
		}
	}
	// A release build gets a checker (it only calls GitHub when invoked).
	if cachedPaladinLatest("v0.3.0", time.Hour) == nil {
		t.Error("release builds must get an update checker")
	}
}
