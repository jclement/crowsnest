// The upgrade path (DESIGN.md 4.18). Migrations only go forward, so before a start that is about
// to change the database's schema, or the first start of a different crowsnest version, serve
// copies the database aside through a read-only connection (nothing in it changes before the
// copy is complete). The newest three such copies are kept; a database written by a newer
// crowsnest is refused before anything is touched, with what to do instead.

package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Upgrade backup names: pre-migration-<session>-<stamp>-s<from>-<version>.db.
const (
	upgradePrefix = "pre-migration-"
	upgradeSuffix = ".db"
	// UpgradeKeep is how many pre-migration copies are kept.
	UpgradeKeep = 3
	// LastVersionKey is the crowsnest version that last started on this database.
	LastVersionKey = "version.last_started"
	// UpdatedFromKey holds the previous version after a start of a different one, until the
	// owner has seen "what changed".
	UpdatedFromKey = "version.updated_from"
)

// UpgradeCheck is what PrepareUpgrade found and did.
type UpgradeCheck struct {
	SchemaVersion int    // the database's schema before this start
	KnownVersion  int    // this binary's
	LastVersion   string // the crowsnest version that last started here ("" unknown)
	BackupPath    string // the copy made, "" when none was needed
}

// ErrNewerDatabase is returned for a database a newer crowsnest wrote.
var ErrNewerDatabase = errors.New("the database was written by a newer crowsnest")

// PrepareUpgrade looks at the database at dbPath read-only and, when this start will migrate it
// or is a different crowsnest version than the last one, copies it into dir first. A missing
// database (a first start) needs nothing.
func PrepareUpgrade(ctx context.Context, dbPath, dir, session, version string, now time.Time) (UpgradeCheck, error) {
	inspection, err := store.Inspect(ctx, dbPath)
	if err != nil {
		return UpgradeCheck{}, err
	}
	check := UpgradeCheck{SchemaVersion: inspection.SchemaVersion, KnownVersion: inspection.KnownVersion}
	if !inspection.Exists || inspection.SchemaVersion == 0 {
		return check, nil
	}
	if inspection.SchemaVersion > inspection.KnownVersion {
		return check, fmt.Errorf("%w: %s is at schema version %d and this crowsnest (%s) knows only %d. "+
			"Run the newer crowsnest again (`crowsnest update`, or reinstall it), or restore a backup made by this version with `crowsnest restore`; "+
			"a copy made before the last upgrade is in %s",
			ErrNewerDatabase, dbPath, inspection.SchemaVersion, version, inspection.KnownVersion, dir)
	}
	if rows, err := store.ReadKVPrefix(ctx, dbPath, LastVersionKey); err == nil {
		check.LastVersion = rows[""]
	}
	if inspection.SchemaVersion == inspection.KnownVersion && check.LastVersion == version {
		return check, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return check, fmt.Errorf("creating the backup directory: %w", err)
	}
	name := fmt.Sprintf("%s%s-%s-s%d-%s%s", upgradePrefix, session, Stamp(now), inspection.SchemaVersion, safeVersion(check.LastVersion), upgradeSuffix)
	check.BackupPath = filepath.Join(dir, name)
	if err := store.VacuumFileInto(ctx, dbPath, check.BackupPath); err != nil {
		return check, err
	}
	if err := os.Chmod(check.BackupPath, 0o600); err != nil {
		return check, fmt.Errorf("making %s private: %w", check.BackupPath, err)
	}
	if _, err := Prune(dir, upgradePrefix+session+"-", upgradeSuffix, UpgradeKeep); err != nil {
		return check, err
	}
	return check, nil
}

// safeVersion makes a version usable in a file name.
func safeVersion(version string) string {
	if version == "" {
		return "unknown"
	}
	out := make([]rune, 0, len(version))
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}

// UpgradeBackups lists the pre-migration copies in dir, newest first.
func UpgradeBackups(dir, session string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].Name()
		if entries[i].Type().IsRegular() && len(name) > len(upgradePrefix) && name[:len(upgradePrefix)] == upgradePrefix &&
			filepath.Ext(name) == upgradeSuffix && hasSessionPrefix(name, session) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out, nil
}

func hasSessionPrefix(name, session string) bool {
	prefix := upgradePrefix + session + "-"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
}
