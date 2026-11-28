// Day-thirty options (DESIGN.md 3, 4.17 to 4.20): how far the Terminal view reaches, sign-in
// with Tailscale, automatic backups and retention. Kept in their own file and loader step so the
// option table's other rows stay as they were.
//
// `--terminal-machines` and `--tailscale-auth` widen who reaches what, so they are flags,
// environment or the config file only: never editable from a browser (3.2), where a stolen
// session could turn them on. The backup and retention numbers are ordinary settings.

package config

import (
	"fmt"
	"time"
)

// Option names for this file's settings.
const (
	optTerminalMachines = "terminal-machines"
	optTailscaleAuth    = "tailscale-auth"
	// optTailscaleAuthLocalRisk is the explicit acceptance --tailscale-auth needs while crowsnest
	// publishes itself (R9-S3). A flag or the environment only, like --tailscale-auth.
	optTailscaleAuthLocalRisk = "tailscale-auth-local-risk"
	optBackupEvery            = "backup-every"
	optBackupKeep             = "backup-keep"
	optArchiveAfterDays       = "archive-after-days"
	optDeleteAfterDays        = "delete-after-days"
)

// Retention and backup defaults. Archiving only hides settled jobs from the boards (they stay
// searchable and nothing is lost), so it starts after two weeks; deleting history is
// irreversible, so its default is half a year, long past any review a settled job could need.
const (
	DefaultBackupKeep       = 7
	DefaultArchiveAfterDays = 14
	DefaultDeleteAfterDays  = 180
	// maxRetentionDays bounds the day counts so a typo cannot overflow a duration.
	maxRetentionDays = 36500
	// minBackupEvery stops an automatic backup from running more often than is useful: each
	// one copies the whole database and the attachments.
	minBackupEvery = time.Hour
)

func init() {
	options = append(options,
		option{name: optTerminalMachines, env: []string{"CROWSNEST_TERMINAL_MACHINES"}, kind: kindBool, def: "false", usage: "let the session terminal's herdr client show your saved SSH machines (off: it gets a state directory of crowsnest's own and sees only this session)"},
		option{name: optTailscaleAuth, env: []string{"CROWSNEST_TAILSCALE_AUTH"}, kind: kindBool, def: "false", usage: "sign in tailnet users the owner allowed, from the identity headers of `tailscale serve` on this machine (password login stays)"},
		option{name: optTailscaleAuthLocalRisk, env: []string{"CROWSNEST_TAILSCALE_AUTH_LOCAL_RISK"}, kind: kindBool, def: "false", usage: "allow --tailscale-auth while crowsnest publishes itself to the tailnet, accepting that any program on this machine could then sign in as the owner"},
		option{name: optBackupEvery, env: []string{"CROWSNEST_BACKUP_EVERY"}, kind: kindDuration, def: "0", usage: "make an automatic backup this often, such as 24h; 0 off"},
		option{name: optBackupKeep, env: []string{"CROWSNEST_BACKUP_KEEP"}, kind: kindInt, def: fmt.Sprint(DefaultBackupKeep), usage: "automatic backups kept; older ones are deleted"},
		option{name: optArchiveAfterDays, env: []string{"CROWSNEST_ARCHIVE_AFTER_DAYS"}, kind: kindInt, def: fmt.Sprint(DefaultArchiveAfterDays), usage: "settled jobs leave the boards after this many days (still searchable); 0 never"},
		option{name: optDeleteAfterDays, env: []string{"CROWSNEST_DELETE_AFTER_DAYS"}, kind: kindInt, def: fmt.Sprint(DefaultDeleteAfterDays), usage: "settled jobs, questions, approvals, journal and spend detail are deleted after this many days; 0 never"},
	)
	editable = append(editable,
		Setting{Name: optBackupEvery, Label: "Automatic backup every", Section: "Backups and storage", Kind: "duration", Apply: ApplyLive,
			Help: "such as 24h; 0 turns automatic backups off. Each copies the database and the attachments"},
		Setting{Name: optBackupKeep, Label: "Automatic backups kept", Section: "Backups and storage", Kind: "int", Apply: ApplyLive,
			Help: "the oldest automatic backup is deleted past this many"},
		Setting{Name: optArchiveAfterDays, Label: "Archive settled jobs after (days)", Section: "Backups and storage", Kind: "int", Apply: ApplyLive,
			Help: "hidden from the boards, still searchable; 0 never"},
		Setting{Name: optDeleteAfterDays, Label: "Delete settled history after (days)", Section: "Backups and storage", Kind: "int", Apply: ApplyLive,
			Help: "jobs and their timelines, settled questions and approvals, journal and spend detail; 0 never. Unlanded work is never deleted without asking"},
	)
}

func (l *loader) loadOps(cfg *Config) error {
	var err error
	if cfg.TerminalMachines, err = l.boolean(optTerminalMachines); err != nil {
		return err
	}
	if cfg.TailscaleAuth, err = l.boolean(optTailscaleAuth); err != nil {
		return err
	}
	if cfg.TailscaleAuthLocalRisk, err = l.boolean(optTailscaleAuthLocalRisk); err != nil {
		return err
	}
	if cfg.TailscaleAuth && cfg.TrustedProxyHeader == HeaderCFConnectingIP {
		// tailscale serve proves nothing about itself: from crowsnest's side its request is a
		// loopback connection with a header, exactly like any other local proxy's (cloudflared)
		// passing on a client's forged one (R5-S6). Reading CF-Connecting-IP says Cloudflare is in
		// front, so the identity header cannot be trusted. (--trusted-proxy loopback alone is how
		// serve itself is set up, so it is allowed; the per-request check refuses a request that
		// carries another proxy's headers.)
		return fmt.Errorf("--tailscale-auth cannot be combined with --trusted-proxy-header cf-connecting-ip: a Cloudflare tunnel on this machine could pass on a forged Tailscale-User-Login; " +
			"use one or the other (DESIGN.md 4.20)")
	}
	if cfg.BackupEvery, err = l.duration(optBackupEvery); err != nil {
		return err
	}
	if cfg.BackupEvery < 0 || (cfg.BackupEvery > 0 && cfg.BackupEvery < minBackupEvery) {
		return fmt.Errorf("--backup-every %s: give 0 (off) or at least 1h", cfg.BackupEvery)
	}
	if cfg.BackupKeep, err = l.integer(optBackupKeep); err != nil {
		return err
	}
	if cfg.BackupKeep < 1 || cfg.BackupKeep > 1000 {
		return fmt.Errorf("--backup-keep %d: keep between 1 and 1000 backups", cfg.BackupKeep)
	}
	if cfg.ArchiveAfterDays, err = l.days(optArchiveAfterDays); err != nil {
		return err
	}
	if cfg.DeleteAfterDays, err = l.days(optDeleteAfterDays); err != nil {
		return err
	}
	if cfg.DeleteAfterDays > 0 && cfg.ArchiveAfterDays > cfg.DeleteAfterDays {
		return fmt.Errorf("--archive-after-days %d is past --delete-after-days %d: a job would be deleted before it is archived", cfg.ArchiveAfterDays, cfg.DeleteAfterDays)
	}
	return nil
}

// days reads a day-count option: 0 (never) or 1 to maxRetentionDays.
func (l *loader) days(name string) (int, error) {
	value, err := l.integer(name)
	if err != nil {
		return 0, err
	}
	if value < 0 || value > maxRetentionDays {
		return 0, fmt.Errorf("--%s %d: give 0 (never) or a number of days up to %d", name, value, maxRetentionDays)
	}
	return value, nil
}

// TerminalClientStateDir is the XDG_STATE_HOME the session terminal's herdr client runs with, or ""
// for the user's own (--terminal-machines).
func (c Config) TerminalClientStateDir() string {
	if c.TerminalMachines || c.StateDir == "" {
		return ""
	}
	return c.StateDir + "/terminal-client"
}

// BackupDir is where automatic and pre-migration backups are written: beside the database,
// under the private data directory.
func (c Config) BackupDir() string { return c.DataDir + "/backups" }
