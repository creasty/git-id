package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creasty/git-id/internal/corpus"
	"github.com/creasty/git-id/internal/ids"
)

// slotDir is where sessions record their slot assignments. A record lives as long as a
// session, so the OS temporary directory is the right place, and it is per user.
var slotDir = func() string {
	name := "git-id"
	if uid := os.Getuid(); uid >= 0 {
		name = fmt.Sprintf("git-id-%d", uid)
	}
	return filepath.Join(os.TempDir(), name, "slots")
}

// slotPath is the record of one session in one working tree. The session ID comes from the
// event, so anything that could leave the directory falls back to a fixed name.
func slotPath(root, sessionID string) string {
	if sessionID == "" || sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\`) {
		sessionID = "default"
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(slotDir(), hex.EncodeToString(sum[:8]), sessionID+".json")
}

// session is one session's slot record and the minting that respects it. Both are opened on
// first use only.
type session struct {
	path string
	repo *repo

	slots  map[string]string
	unlock func()
	minter *ids.Minter
}

// loadSlots reads the record, holding a lock until save so that parallel edits of one
// session agree on what a new slot becomes.
func (s *session) loadSlots() (map[string]string, error) {
	if s.slots != nil {
		return s.slots, nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, err
	}
	unlock, err := lockFile(s.path + ".lock")
	if err != nil {
		return nil, err
	}
	s.unlock = unlock
	s.slots = readRecord(s.path)
	return s.slots, nil
}

// readRecord reads a record without locking it. A missing or broken record is empty: the
// slots it held get new IDs, and integrity reports any reference left pointing at the old ones.
func readRecord(path string) map[string]string {
	slots := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &slots)
	}
	return slots
}

// mint issues an ID avoiding both the repository's bodies and the IDs this session already
// assigned: those may not be written to any file yet, so no scan would find them. Without a
// slot in the edit, the record is only read, so an edit with fresh placeholders writes nothing.
func (s *session) mint(prefix string) (string, error) {
	if s.minter == nil {
		c := corpus.New(s.repo.root, s.repo.cfg)
		s.minter = &ids.Minter{Syntax: s.repo.cfg.Syntax, Load: func() (map[string]struct{}, error) {
			taken, err := c.Taken()
			if err != nil {
				return nil, err
			}
			recorded := s.slots
			if recorded == nil {
				recorded = readRecord(s.path)
			}
			for _, id := range recorded {
				taken[s.repo.cfg.Syntax.BodyOf(id)] = struct{}{}
			}
			return taken, nil
		}}
	}
	return s.minter.Mint(prefix)
}

// save writes the record if an assignment was added, then releases the lock. It runs even
// when the edit is denied: the assignments made so far stand, or the next attempt would give
// the same slot another ID.
func (s *session) save(changed bool) error {
	defer func() {
		if s.unlock != nil {
			s.unlock()
		}
	}()
	if !changed || s.slots == nil {
		return nil
	}
	data, err := json.MarshalIndent(s.slots, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
