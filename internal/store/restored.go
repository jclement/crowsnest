// What a restore brings back but does not trust (R7-S4). A backup is not the human's word on this
// machine: a project's custom check command is a shell command crowsnest runs itself on every
// done, and the instruction layers become every agent's system prompt. So a restore sets each
// custom check command back to automatic and keeps the backup's command aside, and marks each
// saved layer "restored, not reviewed" with the hash of its text. Both are kv rows, so nothing in
// the schema changes; the jobs and instructions services read them, and the human's next save (or
// an explicit "use it") clears them.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	// RestoredCheckPrefix + a project id holds the check command a restore set aside.
	RestoredCheckPrefix = "restored.check_command."
	// RestoredPreparePrefix + a project id holds the isolated check's prepare command a restore
	// set aside (it is a shell command crowsnest runs too, R7-C01).
	RestoredPreparePrefix = "restored.check_prepare."
	// RestoredLayerPrefix + scope + "." + key holds the hash of a layer's text as a restore
	// brought it; while the saved text still has that hash, the layer is withheld from agents.
	RestoredLayerPrefix = "instructions.restored."
)

// RestoredCheck is one project's check command a restore set aside.
type RestoredCheck struct {
	ProjectID int64
	Name      string
	Command   string // "" when only the prepare command was set aside
	Prepare   string
}

// RestoredPrepareKey is the kv key of a project's set-aside prepare command.
func RestoredPrepareKey(projectID int64) string {
	return RestoredPreparePrefix + strconv.FormatInt(projectID, 10)
}

// RestoredLayerKey is the kv key of a restored layer's mark.
func RestoredLayerKey(scope, key string) string { return RestoredLayerPrefix + scope + "." + key }

// RestoredCheckKey is the kv key of a project's set-aside check command.
func RestoredCheckKey(projectID int64) string {
	return RestoredCheckPrefix + strconv.FormatInt(projectID, 10)
}

// LayerTextHash is the hash a restored layer's mark holds: hex SHA-256 of the text.
func LayerTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// SetAsideCheckCommands moves every project's custom check command (anything but automatic and
// "none") and isolated-check prepare command aside into kv, and sets the project back to an
// automatic check with no prepare command. It returns what it moved.
func (s *Store) SetAsideCheckCommands(ctx context.Context) ([]RestoredCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name,
		CASE WHEN trim(check_command) IN ('', 'none') THEN '' ELSE check_command END, check_prepare
		FROM projects WHERE trim(check_command) NOT IN ('', 'none') OR trim(check_prepare) != '' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing check commands: %w", err)
	}
	var moved []RestoredCheck
	for rows.Next() {
		var check RestoredCheck
		if err := rows.Scan(&check.ProjectID, &check.Name, &check.Command, &check.Prepare); err != nil {
			_ = rows.Close()
			return nil, err
		}
		moved = append(moved, check)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, check := range moved {
		if command := strings.TrimSpace(check.Command); command != "" {
			if err := s.SetKV(ctx, RestoredCheckKey(check.ProjectID), command); err != nil {
				return nil, err
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE projects SET check_command = '' WHERE id = ?`, check.ProjectID); err != nil {
				return nil, fmt.Errorf("setting project %d's check back to automatic: %w", check.ProjectID, err)
			}
		}
		if prepare := strings.TrimSpace(check.Prepare); prepare != "" {
			if err := s.SetKV(ctx, RestoredPrepareKey(check.ProjectID), prepare); err != nil {
				return nil, err
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE projects SET check_prepare = '' WHERE id = ?`, check.ProjectID); err != nil {
				return nil, fmt.Errorf("clearing project %d's prepare command: %w", check.ProjectID, err)
			}
		}
	}
	return moved, nil
}

// MarkLayersRestored marks every saved layer with text as restored and not reviewed, and returns
// how many it marked.
func (s *Store) MarkLayersRestored(ctx context.Context) (int, error) {
	layers, err := s.ListInstructionLayers(ctx)
	if err != nil {
		return 0, err
	}
	marked := 0
	for _, layer := range layers {
		if strings.TrimSpace(layer.BodyMD) == "" {
			continue
		}
		if err := s.SetKV(ctx, RestoredLayerKey(layer.Scope, layer.Key), LayerTextHash(layer.BodyMD)); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}
