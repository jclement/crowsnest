// The Storage card (DESIGN.md 4.19, 12.9): what uses how much, the oldest of each, and the
// last clean-up.

package retention

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Area is one thing on disk.
type Area struct {
	Name   string    `json:"name"`
	Label  string    `json:"label"`
	Bytes  int64     `json:"bytes"`
	Count  int       `json:"count"`
	Oldest time.Time `json:"oldest,omitzero"`
	Note   string    `json:"note,omitempty"`
}

// Storage is the Storage card.
type Storage struct {
	Areas   []Area           `json:"areas"`
	Tables  []store.TableUse `json:"tables"`
	Policy  Policy           `json:"policy"`
	LastRun *Report          `json:"last_run"`
	// Worktrees says who cleans worktrees: not this service (the landing branch's sweep).
	Worktrees string `json:"worktrees"`
}

// worktreesNote is shown in place of a worktree figure.
const worktreesNote = "Worktrees are removed by the landing sweep (Jobs, Sweep), which knows what landed; this card never deletes one."

// Storage measures the data directory, the state directory and the database.
func (s *Service) Storage(ctx context.Context) (Storage, error) {
	tables, err := s.opts.Store.Inventory(ctx)
	if err != nil {
		return Storage{}, err
	}
	attachBytes, err := s.opts.Store.AttachmentBytes(ctx)
	if err != nil {
		return Storage{}, err
	}
	out := Storage{Tables: tables, Policy: s.policy(), LastRun: s.LastRun(ctx), Worktrees: worktreesNote}
	out.Areas = append(out.Areas,
		Area{Name: "database", Label: "Database", Bytes: fileSize(s.opts.DatabasePath) + fileSize(s.opts.DatabasePath+"-wal")},
		attachmentArea(tables, attachBytes),
		s.jobDirsArea(),
		backupsArea(s.opts.BackupDir),
	)
	return out, nil
}

func attachmentArea(tables []store.TableUse, bytes int64) Area {
	area := Area{Name: "attachments", Label: "Attachments", Bytes: bytes}
	for _, table := range tables {
		if table.Table == "attachments" {
			area.Count, area.Oldest = table.Rows, table.Oldest
		}
	}
	return area
}

func (s *Service) jobDirsArea() Area {
	root := filepath.Join(s.opts.StateDir, "jobs")
	area := Area{Name: "job_dirs", Label: "Job folders (briefs, files given to agents)"}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if !jobDirName.MatchString(entry.Name()) || !entry.IsDir() {
			continue
		}
		area.Count++
		area.Bytes += dirSize(filepath.Join(root, entry.Name()))
		if info, err := entry.Info(); err == nil && (area.Oldest.IsZero() || info.ModTime().Before(area.Oldest)) {
			area.Oldest = info.ModTime().UTC()
		}
	}
	return area
}

func backupsArea(dir string) Area {
	area := Area{Name: "backups", Label: "Backups (automatic and before upgrades)"}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		area.Count++
		area.Bytes += info.Size()
		if area.Oldest.IsZero() || info.ModTime().Before(area.Oldest) {
			area.Oldest = info.ModTime().UTC()
		}
	}
	return area
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
