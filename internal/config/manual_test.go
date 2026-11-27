package config

import (
	"os"
	"strings"
	"testing"
)

const manualSettingsPath = "../../docs/manual/reference/settings.md"

// TestManualSettingsReference keeps the manual's settings page generated from the tables: a new
// option, a renamed label or a changed default fails here until the page is regenerated with
// UPDATE_MANUAL=1 go test ./internal/config -run Manual.
func TestManualSettingsReference(t *testing.T) {
	want := ManualSettingsMarkdown()
	if os.Getenv("UPDATE_MANUAL") == "1" {
		if err := os.WriteFile(manualSettingsPath, []byte(want), 0o644); err != nil { //nolint:gosec // a committed doc
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(manualSettingsPath)
	if err != nil {
		t.Fatalf("%v (generate it with UPDATE_MANUAL=1 go test ./internal/config -run Manual)", err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date: run UPDATE_MANUAL=1 go test ./internal/config -run Manual", manualSettingsPath)
	}
}

// TestManualSettingsNamesEveryOption checks the generator itself: every flag and every setting
// the app may change is on the page.
func TestManualSettingsNamesEveryOption(t *testing.T) {
	page := ManualSettingsMarkdown()
	for _, row := range FlagTable() {
		if !strings.Contains(page, "`"+row[0]+"`") {
			t.Errorf("the settings page does not name %s", row[0])
		}
	}
	for _, setting := range EditableSettings() {
		if !strings.Contains(page, "| "+setting.Label) {
			t.Errorf("the settings page does not name the setting %q", setting.Label)
		}
	}
}
