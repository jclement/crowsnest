// The diff view (DESIGN.md 6.20): whether Land requires every file of a job's diff to be marked
// viewed. Off by default: the Review tab and Land's confirmation say how many are not viewed,
// as a warning. Kept in its own file and loader step, like accept.go.

package config

const optRequireViewed = "require-viewed-before-land"

func init() {
	options = append(options, option{name: optRequireViewed, env: []string{"CROWSNEST_REQUIRE_VIEWED_BEFORE_LAND"}, kind: kindBool, def: "false",
		usage: "refuse to merge or push a job until every file of its diff is marked viewed (off: Land only warns)"})
	editable = append(editable, Setting{Name: optRequireViewed, Label: "Require all files viewed before Land", Section: "Budgets and acceptance",
		Kind: "bool", Apply: ApplyLive, Help: "merge and push refuse a job whose diff has files not marked viewed; off, Land only warns"})
}

func (l *loader) loadChanges(cfg *Config) error {
	var err error
	cfg.RequireViewedBeforeLand, err = l.boolean(optRequireViewed)
	return err
}
