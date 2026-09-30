package catalog

import (
	"bufio"
	"errors"
	"os"
	"sort"
	"strings"
)

// LockFile records every config ID ever issued. Test results reference config
// IDs, so an ID may be renamed (keeping the old one in `aliases`) but never
// removed or reused. `doioma lock` appends new IDs; nothing ever deletes them.
const LockFile = "config-ids.lock"

const lockHeader = `# Every configuration ID ever issued. Test results reference these IDs.
# Managed by "doioma lock": new IDs are appended, none are ever removed.
# A renamed config keeps its old ID in its "aliases" list.
`

// ReadLock returns the IDs in a lock file. A missing file is an empty lock.
func ReadLock(path string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ids []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			ids = append(ids, line)
		}
	}
	return ids, s.Err()
}

// WriteLock writes the union of the existing lock and ids, sorted. It returns
// the IDs that were newly added.
func WriteLock(path string, ids []string) (added []string, err error) {
	existing, err := ReadLock(path)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, id := range existing {
		set[id] = true
	}
	for _, id := range ids {
		if !set[id] {
			set[id] = true
			added = append(added, id)
		}
	}
	all := make([]string, 0, len(set))
	for id := range set {
		all = append(all, id)
	}
	sort.Strings(all)
	sort.Strings(added)
	return added, os.WriteFile(path, []byte(lockHeader+strings.Join(all, "\n")+"\n"), 0o644)
}
