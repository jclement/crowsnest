// `crowsnest backup`, `crowsnest restore`, the scheduled automatic backup and the copy serve
// takes before an upgrade (DESIGN.md 4.17, 4.18). The format and the checks are internal/backup;
// this file finds what to back up (the database, the attachments, the role prompts, task files
// and risk rules beside the config) and where a restore puts it.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/backup"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
)

// autoBackupPrefix names automatic backups: auto-<session>-<stamp>.tar.gz, pruned by count.
const autoBackupPrefix = "auto-"

// backupSuffix is every backup archive's extension.
const backupSuffix = ".tar.gz"

func newBackupCommand() *cobra.Command {
	var out string
	command := &cobra.Command{
		Use:   "backup",
		Short: "Write a backup: the database (without credentials), attachments, role prompts, task files and risk rules",
		Long: "Writes one .tar.gz with a manifest of versions and checksums. Safe beside a running crowsnest:\n" +
			"the database copy is consistent. Never carries a password in clear or any token; Claude's\n" +
			"transcripts and herdr's state are not included (the manifest says so). Restore with\n" +
			"`crowsnest restore <file>`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := commandConfig(cmd)
			if err != nil {
				return err
			}
			db, err := store.Open(cmd.Context(), cfg.DatabasePath())
			if err != nil {
				return fmt.Errorf("opening %s: %w", cfg.DatabasePath(), err)
			}
			defer func() { _ = db.Close() }()
			if out == "" {
				out = fmt.Sprintf("crowsnest-%s-%s%s", cfg.Session, backup.Stamp(time.Now()), backupSuffix)
			}
			manifest, err := backup.Create(cmd.Context(), backupSource(cfg, db, "manual"), out)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s: %d files, schema %d, crowsnest %s.\nNot included: %s.\n",
				out, len(manifest.Files), manifest.SchemaVersion, manifest.Version, strings.Join(manifest.NotIncluded, "; "))
			return nil
		},
	}
	command.Flags().StringVar(&out, "out", "", "file to write (default crowsnest-<session>-<time>.tar.gz here)")
	return command
}

func newRestoreCommand() *cobra.Command {
	var force, verifyOnly bool
	command := &cobra.Command{
		Use:   "restore <file>",
		Short: "Restore a backup into this machine's data directory; it starts paused",
		Long: "Checks every file in the backup against its manifest, then puts the database, attachments\n" +
			"and config files in place. Refused over a data directory that already holds a database,\n" +
			"and while crowsnest is running, unless --force (the replaced database is kept beside it).\n" +
			"The restored instance starts PAUSED: crowsnest takes stock of the agents herdr really has\n" +
			"before anything is dispatched, and nothing fires until you resume it. Every browser signs\n" +
			"in again and every device turns notifications on again (credentials are never backed up).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if verifyOnly {
				manifest, err := backup.Verify(args[0])
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is intact: %d files, made %s by crowsnest %s (%s), schema %d.\n",
					args[0], len(manifest.Files), manifest.CreatedAt.Local().Format(time.RFC1123), manifest.Version, manifest.Name, manifest.SchemaVersion)
				return nil
			}
			cfg, err := commandConfig(cmd)
			if err != nil {
				return err
			}
			if running := serverRunning(cmd.Context(), cfg); running != "" && !force {
				return fmt.Errorf("crowsnest is running (%s). Stop it first; a restore under a running server would be overwritten by it", running)
			}
			result, err := backup.Restore(cmd.Context(), args[0], restoreTarget(cfg, force))
			if err != nil {
				return err
			}
			printRestore(cmd.OutOrStdout(), cfg, result)
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "replace an existing database (kept as .before-restore-<time>) and existing config files")
	command.Flags().BoolVar(&verifyOnly, "verify", false, "only check the backup against its manifest; write nothing")
	return command
}

func printRestore(out io.Writer, cfg config.Config, result backup.Result) {
	_, _ = fmt.Fprintf(out, "Restored a backup of %q made %s (crowsnest %s) into %s.\n",
		result.Manifest.Name, result.Manifest.CreatedAt.Local().Format(time.RFC1123), result.Manifest.Version, cfg.DatabasePath())
	if result.Replaced != "" {
		_, _ = fmt.Fprintf(out, "The database it replaced is kept at %s.\n", result.Replaced)
	}
	for _, path := range result.Skipped {
		_, _ = fmt.Fprintf(out, "Left %s as it was (it exists; --force replaces it).\n", path)
	}
	_, _ = fmt.Fprintln(out, "It starts PAUSED. Start crowsnest, check the agents on the System page and the journal, then resume.")
}

// commandConfig loads the configuration for a maintenance command (no password needed, no
// --demo, which has no database between runs).
func commandConfig(cmd *cobra.Command) (config.Config, error) {
	flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	cmd.Flags().VisitAll(func(flag *pflag.Flag) { flags.AddFlag(flag) })
	cfg, err := config.Load(flags, config.Options{})
	if err != nil {
		return config.Config{}, err
	}
	if cfg.Demo {
		return config.Config{}, errors.New("--demo keeps no database between runs; there is nothing to back up or restore")
	}
	return cfg, nil
}

// configDir is where roles/, tasks/ and risk.yaml live: beside the config file, else
// ~/.config/crowsnest (XDG). It is the parent of rolesDir.
func configDir(cfg config.Config) string {
	if roles := rolesDir(cfg); roles != "" {
		return filepath.Dir(roles)
	}
	return ""
}

// backupSource lists what a backup carries for cfg.
func backupSource(cfg config.Config, db *store.Store, reason string) backup.Source {
	var extras []backup.Extra
	if dir := configDir(cfg); dir != "" {
		extras = append(extras, markdownExtras(filepath.Join(dir, "roles"), "config/roles/")...)
		extras = append(extras, markdownExtras(filepath.Join(dir, "tasks"), "config/tasks/")...)
		extras = append(extras, backup.Extra{Source: filepath.Join(dir, "risk.yaml"), Archive: "config/risk.yaml"})
	}
	extras = append(extras, markdownExtras(filepath.Join(cfg.Dir, "tasks"), "orchestrator/tasks/")...)
	return backup.Source{
		DB: db, AttachmentsDir: filepath.Join(cfg.DataDir, "attachments"), Extras: extras,
		Version: version, Name: cfg.Name, Session: cfg.Session, Reason: reason, TempDir: cfg.DataDir,
	}
}

// markdownExtras lists the .md files in dir as extras under prefix.
func markdownExtras(dir, prefix string) []backup.Extra {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []backup.Extra
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
			out = append(out, backup.Extra{Source: filepath.Join(dir, entry.Name()), Archive: prefix + entry.Name()})
		}
	}
	return out
}

func restoreTarget(cfg config.Config, force bool) backup.Target {
	return backup.Target{
		DatabasePath: cfg.DatabasePath(), AttachmentsDir: filepath.Join(cfg.DataDir, "attachments"),
		ConfigDir: configDir(cfg), OrchestratorDir: cfg.Dir, Force: force,
	}
}

// ---- Scheduled backups --------------------------------------------------------

// backupScheduler makes an automatic backup every BackupEvery (read live, so the Settings sheet
// takes effect at once) and keeps BackupKeep of them.
type backupScheduler struct {
	cfg     func() config.Config
	db      *store.Store
	journal *journal.Journal
	// paused reports the pause switch (nil: never paused); a due backup waits while it is on.
	paused func() bool
	logger *slog.Logger
}

// backupCheckEvery is how often the scheduler looks whether a backup is due.
const backupCheckEvery = time.Minute

func (b backupScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(backupCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.tick(ctx, time.Now())
		}
	}
}

// tick backs up when the newest automatic backup is older than the interval.
func (b backupScheduler) tick(ctx context.Context, now time.Time) {
	cfg := b.cfg()
	if cfg.BackupEvery <= 0 || cfg.Demo || (b.paused != nil && b.paused()) {
		return
	}
	dir := cfg.BackupDir()
	prefix := autoBackupPrefix + cfg.Session + "-"
	if newest := newestBackup(dir, prefix); !newest.IsZero() && now.Sub(newest) < cfg.BackupEvery {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.logger.Warn("automatic backup: creating the backup directory", "err", err)
		return
	}
	path := filepath.Join(dir, prefix+backup.Stamp(now)+backupSuffix)
	manifest, err := backup.Create(ctx, backupSource(cfg, b.db, "scheduled"), path)
	if err != nil {
		b.logger.Warn("automatic backup failed", "err", err)
		b.record(ctx, "Automatic backup failed: "+err.Error(), nil)
		return
	}
	removed, err := backup.Prune(dir, prefix, backupSuffix, cfg.BackupKeep)
	if err != nil {
		b.logger.Warn("automatic backup: removing old backups", "err", err)
	}
	b.record(ctx, fmt.Sprintf("Automatic backup written (%d files); %d old removed", len(manifest.Files), len(removed)),
		map[string]any{"path": path, "removed": len(removed)})
}

func (b backupScheduler) record(ctx context.Context, summary string, detail map[string]any) {
	if b.journal != nil {
		b.journal.Add(ctx, journal.KindStorage, summary, detail)
	}
}

// newestBackup is the modification time of the newest file in dir named prefix*.tar.gz.
func newestBackup(dir, prefix string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), backupSuffix) {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

// ---- Upgrades -------------------------------------------------------------------

// prepareUpgrade runs before serve opens (and so migrates) the database: a copy when the schema
// or the crowsnest version is about to change, and the refusal of a newer database.
func prepareUpgrade(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if cfg.Demo {
		return nil
	}
	check, err := backup.PrepareUpgrade(ctx, cfg.DatabasePath(), cfg.BackupDir(), cfg.Session, version, time.Now())
	if err != nil {
		return err
	}
	if check.BackupPath != "" {
		logger.Info("copied the database before this upgrade", "path", check.BackupPath,
			"schema", check.SchemaVersion, "to_schema", check.KnownVersion, "from_version", check.LastVersion, "to_version", version)
	}
	return nil
}

// recordVersion notes this start's version, and the previous one when it changed, for the
// "what changed" notice (GET /api/v1/updated).
func recordVersion(ctx context.Context, db *store.Store, logger *slog.Logger) {
	last, _ := db.GetKV(ctx, backup.LastVersionKey)
	if last == version {
		return
	}
	if last != "" {
		body, _ := json.Marshal(map[string]string{"from": last, "to": version, "at": time.Now().UTC().Format(time.RFC3339)})
		if err := db.SetKV(ctx, backup.UpdatedFromKey, string(body)); err != nil {
			logger.Warn("recording the update", "err", err)
		}
	}
	if err := db.SetKV(ctx, backup.LastVersionKey, version); err != nil {
		logger.Warn("recording this version", "err", err)
	}
}
