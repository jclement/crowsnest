package schedule

import (
	"strings"
	"testing"
	"time"
)

// edmonton has DST: 2026-03-08 02:00 → 03:00 and 2026-11-01 02:00 → 01:00.
func edmonton(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Edmonton")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	return loc
}

func mustTask(t *testing.T, front string) Task {
	t.Helper()
	task, err := Parse("/tasks/check.md", OriginDir, []byte("---\n"+front+"\n---\n# Check things\nDo it.\n"))
	if err != nil {
		t.Fatalf("parse %q: %v", front, err)
	}
	return task
}

func TestCheckCadences(t *testing.T) {
	loc := edmonton(t)
	at := func(text string) time.Time {
		parsed, err := time.ParseInLocation("2006-01-02 15:04", text, loc)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	// The fall-back hour happens twice: 01:10 MDT and then 01:15 MST, 65 minutes later.
	firstOneTen := time.Date(2026, 11, 1, 7, 10, 0, 0, time.UTC)   // 01:10 MDT
	secondOneFifteen := time.Date(2026, 11, 1, 8, 15, 0, 0, time.UTC) // 01:15 MST

	cases := []struct {
		name    string
		front   string
		now     time.Time
		stamp   Stamp
		enabled bool
		picture string
		want    Verdict
	}{
		{"15m never run", "every: 15m", at("2026-11-26 09:00"), Stamp{}, true, "", Due},
		{"15m too soon", "every: 15m", at("2026-11-26 09:10"), Stamp{LastRun: at("2026-11-26 09:00")}, true, "", NotDue},
		{"15m on time", "every: 15m", at("2026-11-26 09:15"), Stamp{LastRun: at("2026-11-26 09:00")}, true, "", Due},
		{"60m", "every: 60m", at("2026-11-26 09:59"), Stamp{LastRun: at("2026-11-26 09:00")}, true, "", NotDue},
		{"4h elapsed", "every: 4h", at("2026-11-26 13:00"), Stamp{LastRun: at("2026-11-26 09:00")}, true, "", Due},
		{"disabled", "every: 15m", at("2026-11-26 09:00"), Stamp{}, false, "", NotDue},
		{"disabled but run now", "every: 15m", at("2026-11-26 09:01"), Stamp{LastRun: at("2026-11-26 09:00"), RunNow: true}, false, "", Due},
		{"1d before after", "every: 1d\nafter: 17:00", at("2026-11-26 16:59"), Stamp{LastRun: at("2026-11-25 17:01")}, true, "", NotDue},
		{"1d after after", "every: 1d\nafter: 17:00", at("2026-11-26 17:00"), Stamp{LastRun: at("2026-11-25 17:01")}, true, "", Due},
		{"1d twice in a day", "every: 1d\nafter: 17:00", at("2026-11-26 23:00"), Stamp{LastRun: at("2026-11-26 17:01")}, true, "", NotDue},
		{"1d just after midnight, no after", "every: 1d", at("2026-11-27 00:01"), Stamp{LastRun: at("2026-11-26 23:59")}, true, "", Due},
		{"3d", "every: 3d", at("2026-11-28 08:00"), Stamp{LastRun: at("2026-11-26 08:00")}, true, "", NotDue},
		{"3d due", "every: 3d", at("2026-11-29 00:30"), Stamp{LastRun: at("2026-11-26 23:00")}, true, "", Due},
		{"spring forward: after in the missing hour", "every: 1d\nafter: 02:30", at("2026-03-08 03:05"), Stamp{LastRun: at("2026-03-07 02:35")}, true, "", Due},
		{"spring forward: before", "every: 1d\nafter: 03:30", at("2026-03-08 03:05"), Stamp{LastRun: at("2026-03-07 03:35")}, true, "", NotDue},
		{"fall back: same day twice is once", "every: 1d\nafter: 01:00", secondOneFifteen, Stamp{LastRun: firstOneTen}, true, "", NotDue},
		{"poke rides along", "every: poke", at("2026-11-26 09:00"), Stamp{}, true, "", RideAlong},
		{"poke disabled", "every: poke", at("2026-11-26 09:00"), Stamp{}, false, "", NotDue},
		{"poke run now is due on its own", "every: poke", at("2026-11-26 09:00"), Stamp{RunNow: true}, true, "", Due},
		{"poke after", "every: poke\nafter: 10:00", at("2026-11-26 09:00"), Stamp{}, true, "", NotDue},
		{"change first time", "every: change", at("2026-11-26 09:00"), Stamp{}, true, "abc", Due},
		{"change same picture", "every: change", at("2026-11-26 10:00"), Stamp{LastRun: at("2026-11-26 09:00"), Picture: "abc"}, true, "abc", NotDue},
		{"change new picture", "every: change", at("2026-11-26 10:00"), Stamp{LastRun: at("2026-11-26 09:00"), Picture: "abc"}, true, "def", Due},
		{"change too soon", "every: change", at("2026-11-26 09:04"), Stamp{LastRun: at("2026-11-26 09:00"), Picture: "abc"}, true, "def", NotDue},
		{"change picture unknown", "every: change", at("2026-11-26 10:00"), Stamp{}, true, "", NotDue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := mustTask(t, tc.front)
			got := Check(task, DueInput{Now: tc.now, Location: loc, Stamp: tc.stamp, Enabled: tc.enabled, Picture: tc.picture})
			if got != tc.want {
				t.Fatalf("Check = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNextDue(t *testing.T) {
	loc := edmonton(t)
	at := func(text string) time.Time {
		parsed, _ := time.ParseInLocation("2006-01-02 15:04", text, loc)
		return parsed
	}
	now := at("2026-11-26 09:00")
	cases := []struct {
		front string
		stamp Stamp
		want  time.Time
	}{
		{"every: 15m", Stamp{LastRun: at("2026-11-26 08:50")}, at("2026-11-26 09:05")},
		{"every: 15m", Stamp{LastRun: at("2026-11-26 08:00")}, now}, // overdue: now
		{"every: 1d\nafter: 17:00", Stamp{LastRun: at("2026-11-25 17:02")}, at("2026-11-26 17:00")},
		{"every: 1d\nafter: 17:00", Stamp{LastRun: at("2026-11-26 17:02")}, at("2026-11-27 17:00")},
		{"every: 1d\nafter: 17:00", Stamp{}, at("2026-11-26 17:00")},
		{"every: poke", Stamp{}, time.Time{}},
		{"every: change", Stamp{}, time.Time{}},
	}
	for _, tc := range cases {
		got := NextDue(mustTask(t, tc.front), DueInput{Now: now, Location: loc, Stamp: tc.stamp, Enabled: true})
		if !got.Equal(tc.want) {
			t.Errorf("%q %+v: NextDue = %v, want %v", tc.front, tc.stamp, got, tc.want)
		}
	}
	if got := NextDue(mustTask(t, "every: 15m"), DueInput{Now: now, Location: loc}); !got.IsZero() {
		t.Errorf("a disabled task has no next time, got %v", got)
	}
}

func TestParse(t *testing.T) {
	good := []struct {
		name, content string
		check         func(Task) bool
	}{
		{"front door file", "---\nevery: 1d\nafter: 06:30\n---\n\n# Daily digest\n\n1. Do it.\n", func(task Task) bool {
			return task.Every.Kind == CadenceDays && task.After.Minutes == 390 && task.Title == "Daily digest" && task.As == RunAsPoke
		}},
		{"comments and CRLF", "---\r\nevery: 30m   # half-hourly\r\n---\r\nbody\r\n", func(task Task) bool {
			return task.Every.Interval == 30*time.Minute && task.Body == "body"
		}},
		{"as job", "---\nevery: 4h\nas: job\ndir: /tmp/x\nrole: builder\n---\nBuild it.\n", func(task Task) bool {
			return task.As == RunAsJob && task.Dir == "/tmp/x" && task.Role == "builder"
		}},
	}
	for _, tc := range good {
		task, err := Parse("/t/"+"ok.md", OriginUser, []byte(tc.content))
		if err != nil || !tc.check(task) {
			t.Errorf("%s: got %+v, %v", tc.name, task, err)
		}
	}
	bad := []struct{ path, content, want string }{
		{"/t/Bad Name.md", "---\nevery: 1d\n---\n", "file names"},
		{"/t/x.md", "every: 1d\n", "must start"},
		{"/t/x.md", "---\nevery: 1d\n", "not closed"},
		{"/t/x.md", "---\nevry: 1d\n---\n", "unknown frontmatter key"},
		{"/t/x.md", "---\nevery: 1w\n---\n", "not understood"},
		{"/t/x.md", "---\nevery: 1m\n---\n", "too often"},
		{"/t/x.md", "---\nevery: 0h\n---\n", "more than zero"},
		{"/t/x.md", "---\nevery: 1d\nafter: 25:00\n---\n", "not a time"},
		{"/t/x.md", "---\nevery: 1d\nafter: noon\n---\n", "HH:MM"},
		{"/t/x.md", "---\nevery: 1d\nas: robot\n---\n", "not poke or job"},
		{"/t/x.md", "---\nevery: 1d\ndir: /x\n---\n", "only to a task with as: job"},
		{"/t/x.md", "---\nevery: poke\nas: job\n---\nx", "cannot be as: job"},
		{"/t/x.md", "---\nevery: 1d\nas: job\n---\n", "needs a body"},
		{"/t/x.md", "---\n---\n", "every: is missing"},
		{"/t/x.md", "---\nevery: 1d\nevery: 2d\n---\n", "twice"},
	}
	for _, tc := range bad {
		if _, err := Parse(tc.path, OriginDir, []byte(tc.content)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %v, want it to mention %q", tc.content, err, tc.want)
		}
	}
}
