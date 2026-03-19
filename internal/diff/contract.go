package diff

// DiffContract is the universal output contract of any code step. At this milestone
// it is produced by the snapshot mechanism, not by an agent. Consumers (apply, report)
// rely on base/head commits and patch to reason about what changed.
type DiffContract struct {
	BaseCommit   string   // hash of commit before the step
	HeadCommit   string   // hash of commit after the step (snapshot)
	Patch        string   // unified diff content
	FilesChanged []string // list of modified/created/deleted files
}
