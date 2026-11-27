package config

import "testing"

// TestThemeSetting: the default theme is saved in the app like any setting, read back as given
// (lower-cased), and refused when it is not a theme id's shape (it is written into index.html).
func TestThemeSetting(t *testing.T) {
	for _, tc := range []struct {
		stored, want string
		ok           bool
	}{
		{"", "", true},
		{"phosphor", "phosphor", true},
		{" Deep-Sea ", "deep-sea", true},
		{`x"><script>`, "", false},
		{"-dash-first", "", false},
		{"a very long name", "", false},
	} {
		h := newHarness(t)
		opts := h.options()
		opts.Stored = map[string]string{"theme": tc.stored}
		opts.StrictStored = true // as the app validates a change
		cfg, err := Load(h.flags(), opts)
		if (err == nil) != tc.ok {
			t.Errorf("theme %q: err %v, want ok=%v", tc.stored, err, tc.ok)
			continue
		}
		if tc.ok && cfg.SettingValue("theme") != tc.want {
			t.Errorf("theme %q: setting value %q, want %q", tc.stored, cfg.SettingValue("theme"), tc.want)
		}
	}
	if !Editable("theme") {
		t.Error("the default theme must be editable in the app")
	}
}

// TestBadStoredThemeDoesNotStopTheStart is R7-I5: a bad theme in the database (a restored kv)
// is a warning and the house theme at start, not a failed start.
func TestBadStoredThemeDoesNotStopTheStart(t *testing.T) {
	h := newHarness(t)
	opts := h.options()
	opts.Stored = map[string]string{"theme": `x"><script>`}
	cfg, err := Load(h.flags(), opts)
	if err != nil {
		t.Fatalf("the start failed on a stored theme: %v", err)
	}
	if cfg.Theme != "" || len(cfg.Warnings) == 0 {
		t.Fatalf("theme %q, warnings %v", cfg.Theme, cfg.Warnings)
	}
}
