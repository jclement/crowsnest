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
