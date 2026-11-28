package config

import "testing"

// R9-S2: every setting the app may change has a restore rule, in exactly one list. A new setting
// fails here until someone decides whether a backup may bring it back.
func TestEverySettingHasARestoreRule(t *testing.T) {
	for _, setting := range EditableSettings() {
		_, aside := restoreSetAside[setting.Name]
		_, kept := restoreKept[setting.Name]
		switch {
		case !aside && !kept:
			t.Errorf("setting %q has no restore rule: add it to restoreSetAside (it acts outward or grants) or restoreKept (with why that is safe), in restore.go", setting.Name)
		case aside && kept:
			t.Errorf("setting %q is in both restore lists", setting.Name)
		}
	}
	for name := range restoreSetAside {
		if !Editable(name) {
			t.Errorf("restoreSetAside names %q, which is not an app setting", name)
		}
	}
	for name := range restoreKept {
		if !Editable(name) {
			t.Errorf("restoreKept names %q, which is not an app setting", name)
		}
	}
	if _, ok := RestoreSetsAside("tailscale-serve"); !ok {
		t.Error("publishing must be set aside by a restore")
	}
}
