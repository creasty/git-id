//go:build !unix

package hook

// lockFile does not lock where flock is unavailable: parallel edits of one session may then
// give a new slot two IDs, which `git id integrity` reports as a stray reference.
func lockFile(string) (func(), error) { return func() {}, nil }
