// What a restore does with each setting saved in the app (R9-S2). A backup is not the human's word
// on this machine: one from another instance, or a crafted one, could bring its owner a way in or
// switch a gate off. So every setting that makes crowsnest act outward (publish itself, post to a
// URL, open its agents to the Claude app) or grant (agents may do more, a human gate is lifted, a
// protection is dropped) is set aside by a restore: its saved value stops applying (the file, then
// the default, apply instead), and the Settings view shows the backup's value with "Use it" until
// the human decides. Everything else comes back as it was.
//
// This file is the one place that says which is which. TestEverySettingHasARestoreRule fails for a
// new app setting until it is added to one of the two lists here, with its reason.

package config

// restoreSetAside are the settings a restore sets aside, each with why.
var restoreSetAside = map[string]string{
	optTailscaleServe:      "publishes crowsnest to the tailnet at the next start (outward)",
	optNotifyURL:           "every notice is posted to it (outward; already scrubbed from new backups)",
	optNoRemoteControl:     "Remote Control opens the orchestrator to the Claude app (outward)",
	optJobRemoteControl:    "Remote Control opens job agents to the Claude app (outward)",
	optPermissionMode:      "bypassPermissions or auto lets the orchestrator act without asking (grant)",
	optProtectedHost:       "an emptied list lets agents reach production hosts unattended (grant)",
	optProtectedBranch:     "an emptied list lets agents commit onto protected branches (grant)",
	optReview:              "off lifts the review gate before Land (grant)",
	optRequireViewed:       "off lifts the viewed-files gate before Land (grant)",
	optTrustProjectFolders: "writes the agents' own trust records for new projects (grant)",
}

// restoreKept are the settings a restore brings back as they were, each with why that is safe.
//
//nolint:gosec // G101: option names and reasons, not credentials
var restoreKept = map[string]string{
	optModel:                "a model choice: no reach, no gate",
	optJobModel:             "a model choice: no reach, no gate",
	optEngine:               "which agent runs: every engine is supervised the same way",
	optNoInstructionRestart: "upkeep timing",
	optPokeInterval:         "upkeep timing",
	optCompactAt:            "upkeep timing",
	optFreshAt:              "upkeep timing",
	optAttachmentMaxMB:      "a size limit",
	optAttachmentQuotaMB:    "a size limit",
	optMaxRunningJobs:       "throughput, bounded by the machine",
	optLoadHold:             "throughput",
	optHeavySlots:           "throughput",
	optMerge:                "how Land merges, still behind Land's own gates",
	optDigestAt:             "a time of day",
	optTheme:                "appearance",
	optBackupEvery:          "local backups",
	optBackupKeep:           "local backups",
	optArchiveAfterDays:     "local retention",
	optDeleteAfterDays:      "local retention",
	optJobBudgetUSD:         "a budget only stops work, never grants",
	optJobBudgetMinutes:     "a budget only stops work, never grants",
	optJobBudgetTurns:       "a budget only stops work, never grants",
	optJobBudgetTokens:      "a budget only stops work, never grants",
	optDailyCeilingUSD:      "a ceiling only stops work, never grants",
	optOrchCeilingUSD:       "a ceiling only stops work, never grants",
	optLeadCeilingUSD:       "a ceiling only stops work, never grants",
	optCheckTimeout:         "the check's time limit (a restore already sets custom check commands aside)",
	optCheckRounds:          "how many check rounds before the human is asked",
}

// RestoreSetAside lists the settings a restore sets aside (R9-S2).
func RestoreSetAside() []string {
	names := make([]string, 0, len(restoreSetAside))
	for name := range restoreSetAside {
		names = append(names, name)
	}
	return names
}

// RestoreSetsAside reports whether a restore sets the setting aside, and why.
func RestoreSetsAside(name string) (string, bool) {
	why, ok := restoreSetAside[name]
	return why, ok
}
