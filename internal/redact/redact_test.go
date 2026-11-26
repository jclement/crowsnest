package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	cases := []struct {
		in       string
		mustHide string
		keep     string
	}{
		{"push with ghp_abcdefghijklmnopqrstuvwxyz0123", "ghp_abc", "push with"},
		{"Authorization: Bearer abcdef0123456789xyz", "abcdef0123456789xyz", "Bearer"},
		{"password=hunter2 please", "hunter2", "password"},
		{"API_KEY: sk-live-abcdefghijklmnop", "sk-live", "API_KEY"},
		{"jwt eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4", "eyJhbGci", "jwt"},
		{"clone https://user:pa55word@github.com/x", "pa55word", "github.com/x"},
		{"key 3f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a", "3f9a8b7c6d5e", "key"},
		{"aws AKIAABCDEFGHIJKLMNOP", "AKIAABCDEFGHIJKLMNOP", "aws"},
	}
	for _, tc := range cases {
		got := String(tc.in)
		if strings.Contains(got, tc.mustHide) || !strings.Contains(got, tc.keep) || !strings.Contains(got, Placeholder) {
			t.Errorf("String(%q) = %q", tc.in, got)
		}
	}
	for _, plain := range []string{"Ship the migration tonight?", "internal/fleet/cache.go line 42", "q_12 needs you"} {
		if got := String(plain); got != plain {
			t.Errorf("plain text changed: %q → %q", plain, got)
		}
	}
}
