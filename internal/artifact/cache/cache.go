// Package cache provides the disposable, fingerprint-validated cache shared by
// runtime metadata and flat/store image indexing.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

const (
	FileName      = "cnt-images-v2.json.gz"
	schemaVersion = 2
)

// Fingerprint identifies one inode generation. Cache hits require every field
// available from lstat to agree. The device number is not part of it: a
// network filesystem reports a different one for each mount.
type Fingerprint struct {
	Inode uint64 `json:"inode,omitempty"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	CTime int64  `json:"ctime,omitempty"`
}

// Key is a neutral serialization of a complete artifact key.
type Key struct {
	Scheme string `json:"scheme"`
	SHA256 string `json:"sha256"`
}

// Record shares one fingerprint across independently populated fields.
// RuntimeKnown distinguishes an absent runtime document from an unprobed one.
type Record struct {
	Fingerprint  Fingerprint     `json:"fingerprint"`
	RuntimeKnown bool            `json:"runtime_known,omitempty"`
	Runtime      json.RawMessage `json:"runtime,omitempty"`
	Name         string          `json:"name,omitempty"`
	Identity     Key             `json:"identity,omitempty"`
	Equiv        Key             `json:"equiv,omitempty"`
	KeysVerified bool            `json:"keys_verified,omitempty"`
}

type diskCache struct {
	Version int               `json:"version"`
	Records map[string]Record `json:"records"`
}

// Cache is safe for concurrent goroutines. Writers also take a sibling flock,
// reload, merge, and atomically publish so separate processes do not erase one
// another's fields. A writer that cannot get the lock in time keeps its update in
// memory and does not persist it.
type Cache struct {
	mu      sync.Mutex
	pathFn  func() string
	path    string
	loaded  bool
	records map[string]Record
	// busy is set when the file lock could not be had in time. The process then
	// stops trying, so a stuck lock costs one wait, not one per write.
	busy bool
	// accept says which paths may be recorded. Nil accepts every path.
	accept func(path string) bool
}

// Access is the cache surface used by metadata consumers. Both Cache and Batch
// implement it, so directory scans can defer persistence until their end.
type Access interface {
	Lookup(path string, info os.FileInfo) (Record, bool)
	Merge(path string, info os.FileInfo, update func(*Record))
	Forget(path string)
}

// Update is one field-preserving cache mutation.
type Update struct {
	Path  string
	Info  os.FileInfo
	Apply func(*Record)
}

// Batch collects mutations and commits them with one lock, reload, and atomic
// gzip rewrite. Lookups still see records committed before the batch began.
type Batch struct {
	cache   *Cache
	updates []Update
	forget  []string
}

var defaultCache = newDefault()

// newDefault is the process-wide cache. It records only files a scan reads: those directly in an image root or in its store directory.
func newDefault() *Cache {
	c := New(defaultPath)
	c.accept = func(path string) bool { return underRoots(config.GetImageSearchPaths(), path) }
	return c
}

// storeDirName is the overflow directory below an image root. It matches store.DirName, which this package cannot import.
const storeDirName = "store"

// underRoots reports whether path sits directly in one of roots, or directly in a root's store directory.
func underRoots(roots []string, path string) bool {
	dir := filepath.Dir(cleanAbsolute(path))
	for _, root := range roots {
		root = cleanAbsolute(root)
		if dir == root || dir == filepath.Join(root, storeDirName) {
			return true
		}
	}
	return false
}

// New constructs a cache whose path is resolved on first use. An empty path
// disables persistence while retaining a process-local cache.
func New(pathFn func() string) *Cache { return &Cache{pathFn: pathFn} }

func defaultPath() string {
	dir, err := config.GetWritableCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, FileName)
}

// Default returns the process-wide image cache.
func Default() *Cache { return defaultCache }

// NewBatch creates a write batch over c. Flush must be called to persist its
// mutations; an abandoned batch merely loses cache hints.
func (c *Cache) NewBatch() *Batch { return &Batch{cache: c} }

// Forget removes a path from the process-wide image cache.
func Forget(path string) { defaultCache.Forget(path) }

// Lookup returns a record only for a regular non-symlink file with a matching
// fingerprint.
func (c *Cache) Lookup(path string, info os.FileInfo) (Record, bool) {
	abs := cleanAbsolute(path)
	fingerprint, ok := fingerprint(info)
	if !ok {
		return Record{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	record, found := c.records[abs]
	return record, found && record.Fingerprint == fingerprint
}

// Merge updates selected fields without discarding fields another consumer or
// process populated for the same inode generation. Persistence failure is
// deliberately ignored: cache state is never required for correctness.
func (c *Cache) Merge(path string, info os.FileInfo, update func(*Record)) {
	c.MergeBatch([]Update{{Path: path, Info: info, Apply: update}})
}

// MergeBatch applies valid updates without discarding fields concurrently
// populated by another process, then persists once.
func (c *Cache) MergeBatch(updates []Update) {
	prepared := prepare(updates)
	if len(prepared) == 0 {
		return
	}
	c.commit(prepared, nil)
}

func (c *Cache) commit(updates []preparedUpdate, forget []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	updates = c.accepted(updates)
	if c.path == "" || c.busy {
		c.apply(updates)
		c.remove(forget)
		return
	}
	persisted := c.withDiskLock(func(latest map[string]Record) {
		c.records = latest
		changed := c.apply(updates)
		if c.remove(forget) {
			changed = true
		}
		if changed {
			c.save()
		}
	})
	if !persisted {
		c.busy = true
		c.apply(updates)
		c.remove(forget)
	}
}

// Forget eagerly removes path. A later inode replacement would also miss by
// fingerprint, so a failed cache write remains safe.
func (c *Cache) Forget(path string) {
	c.forgetBatch([]string{path})
}

func (c *Cache) forgetBatch(paths []string) {
	abs := make([]string, 0, len(paths))
	for _, path := range paths {
		abs = append(abs, cleanAbsolute(path))
	}
	c.commit(nil, abs)
}

type preparedUpdate struct {
	path  string
	fp    Fingerprint
	apply func(*Record)
}

func prepare(updates []Update) []preparedUpdate {
	prepared := make([]preparedUpdate, 0, len(updates))
	for _, update := range updates {
		fp, ok := fingerprint(update.Info)
		if !ok || update.Apply == nil {
			continue
		}
		prepared = append(prepared, preparedUpdate{path: cleanAbsolute(update.Path), fp: fp, apply: update.Apply})
	}
	return prepared
}

// accepted drops the updates for paths the cache does not record.
func (c *Cache) accepted(updates []preparedUpdate) []preparedUpdate {
	if c.accept == nil {
		return updates
	}
	kept := updates[:0:0]
	for _, update := range updates {
		if c.accept(update.path) {
			kept = append(kept, update)
		}
	}
	return kept
}

// apply merges the updates into the records and reports whether any record changed.
func (c *Cache) apply(updates []preparedUpdate) bool {
	changed := false
	for _, update := range updates {
		before, existed := c.records[update.path]
		record := c.current(update.path, update.fp)
		update.apply(&record)
		if !existed || !reflect.DeepEqual(before, record) {
			changed = true
		}
		c.records[update.path] = record
	}
	return changed
}

// remove drops the records of paths and reports whether it dropped any.
func (c *Cache) remove(paths []string) bool {
	removed := false
	for _, path := range paths {
		if _, ok := c.records[path]; ok {
			delete(c.records, path)
			removed = true
		}
	}
	return removed
}

// PruneMissing opportunistically removes records whose path is absent or no
// longer has the recorded fingerprint. It is never needed for correctness.
func (c *Cache) PruneMissing() int {
	c.mu.Lock()
	c.load()
	snapshot := make(map[string]Record, len(c.records))
	for path, record := range c.records {
		snapshot[path] = record
	}
	c.mu.Unlock()

	stale := make([]string, 0)
	for path, record := range snapshot {
		info, err := os.Lstat(path)
		if err != nil {
			stale = append(stale, path)
			continue
		}
		current, ok := fingerprint(info)
		if !ok || current != record.Fingerprint {
			stale = append(stale, path)
		}
	}
	if len(stale) != 0 {
		c.forgetBatch(stale)
	}
	return len(stale)
}

// Lookup implements Access without exposing uncommitted mutations.
func (b *Batch) Lookup(path string, info os.FileInfo) (Record, bool) {
	return b.cache.Lookup(path, info)
}

// Merge queues one mutation for Flush.
func (b *Batch) Merge(path string, info os.FileInfo, update func(*Record)) {
	abs := cleanAbsolute(path)
	kept := b.forget[:0]
	for _, forgotten := range b.forget {
		if cleanAbsolute(forgotten) != abs {
			kept = append(kept, forgotten)
		}
	}
	b.forget = kept
	b.updates = append(b.updates, Update{Path: path, Info: info, Apply: update})
}

// Forget queues one removal for Flush.
func (b *Batch) Forget(path string) {
	abs := cleanAbsolute(path)
	kept := b.updates[:0]
	for _, update := range b.updates {
		if cleanAbsolute(update.Path) != abs {
			kept = append(kept, update)
		}
	}
	b.updates = kept
	b.forget = append(b.forget, path)
}

// Flush commits every queued mutation with one lock, reload, and atomic rewrite.
func (b *Batch) Flush() {
	updates := prepare(b.updates)
	forget := make([]string, 0, len(b.forget))
	for _, path := range b.forget {
		forget = append(forget, cleanAbsolute(path))
	}
	if len(updates) != 0 || len(forget) != 0 {
		b.cache.commit(updates, forget)
	}
	b.updates = nil
	b.forget = nil
}

func (c *Cache) current(path string, fp Fingerprint) Record {
	record, ok := c.records[path]
	if !ok || record.Fingerprint != fp {
		return Record{Fingerprint: fp}
	}
	return record
}

func (c *Cache) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.records = map[string]Record{}
	if c.pathFn != nil {
		c.path = c.pathFn()
	}
	if c.path == "" {
		return
	}
	c.records = readRecords(c.path)
}

func (c *Cache) save() {
	if c.path != "" {
		_ = utils.WriteGzipJSONFileAtomic(c.path, diskCache{Version: schemaVersion, Records: c.records})
	}
}

// lockWait is how long a writer waits for the file lock before giving up on persisting.
var lockWait = time.Second

// lockPoll is how often a waiting writer retries the lock.
const lockPoll = 5 * time.Millisecond

// withDiskLock runs fn on the latest records under the file lock, and reports whether it ran.
//   - A writer that cannot get the lock within lockWait skips the write: every record can be regenerated, so a held lock never stalls a command.
func (c *Cache) withDiskLock(fn func(map[string]Record)) bool {
	lock, err := os.OpenFile(c.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer lock.Close() //nolint:errcheck
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK || !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(lockPoll)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	fn(readRecords(c.path))
	return true
}

func readRecords(path string) map[string]Record {
	var disk diskCache
	if err := utils.ReadGzipJSONFile(path, &disk); err != nil || disk.Version != schemaVersion || disk.Records == nil {
		return map[string]Record{}
	}
	return disk.Records
}

func fingerprint(info os.FileInfo) (Fingerprint, bool) {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Fingerprint{}, false
	}
	fp := Fingerprint{Size: info.Size(), MTime: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		fp.Inode = stat.Ino
		fp.CTime = stat.Ctim.Sec*1e9 + stat.Ctim.Nsec
	}
	return fp, true
}

func cleanAbsolute(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
