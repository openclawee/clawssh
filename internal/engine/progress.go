package engine

// ProgressReporter is implemented by intent engines that benefit from a short SSH banner
// while waiting on a remote model (LLM). Keyword routers typically return false.
type ProgressReporter interface {
	ShowProgress() bool
}
