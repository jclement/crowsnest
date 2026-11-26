// Attachment limits (DESIGN.md 3 and 8.6). In their own file and loader step, as upkeep's are,
// so the option table's other rows stay as they were.

package config

import "fmt"

// Option names for attachments.
const (
	optAttachmentMaxMB   = "attachment-max-mb"
	optAttachmentQuotaMB = "attachment-quota-mb"
)

// Defaults: 25 MB a file, 1 GB in all.
const (
	DefaultAttachmentMaxMB   = 25
	DefaultAttachmentQuotaMB = 1024
)

func init() {
	options = append(options,
		option{name: optAttachmentMaxMB, env: []string{"CROWSNEST_ATTACHMENT_MAX_MB"}, kind: kindInt, def: fmt.Sprint(DefaultAttachmentMaxMB), usage: "largest attachment (pasted, dropped or attached by an agent) in MB"},
		option{name: optAttachmentQuotaMB, env: []string{"CROWSNEST_ATTACHMENT_QUOTA_MB"}, kind: kindInt, def: fmt.Sprint(DefaultAttachmentQuotaMB), usage: "disk all attachments may take, in MB; past it the oldest files nobody needs are deleted"},
	)
}

func (l *loader) loadAttachments(cfg *Config) error {
	var err error
	if cfg.AttachmentMaxMB, err = l.integer(optAttachmentMaxMB); err != nil {
		return err
	}
	if cfg.AttachmentQuotaMB, err = l.integer(optAttachmentQuotaMB); err != nil {
		return err
	}
	if cfg.AttachmentMaxMB < 1 || cfg.AttachmentMaxMB > 1024 {
		return fmt.Errorf("--attachment-max-mb %d: give a size from 1 to 1024 MB", cfg.AttachmentMaxMB)
	}
	if cfg.AttachmentQuotaMB < cfg.AttachmentMaxMB {
		return fmt.Errorf("--attachment-quota-mb %d is smaller than --attachment-max-mb %d; one file must fit", cfg.AttachmentQuotaMB, cfg.AttachmentMaxMB)
	}
	return nil
}
