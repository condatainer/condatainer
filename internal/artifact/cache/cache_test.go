package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestCacheMergesFieldsAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	image := filepath.Join(dir, "x.sqf")
	if err := os.WriteFile(image, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(image)
	if err != nil {
		t.Fatal(err)
	}
	first := New(func() string { return cachePath })
	second := New(func() string { return cachePath })
	first.Merge(image, info, func(record *Record) {
		record.RuntimeKnown = true
		record.Runtime = json.RawMessage(`{"name":"x"}`)
	})
	second.Merge(image, info, func(record *Record) {
		record.Name = "x/1"
		record.Identity = Key{Scheme: "identity-v1", SHA256: "abc"}
	})

	reloaded := New(func() string { return cachePath })
	record, ok := reloaded.Lookup(image, info)
	if !ok {
		t.Fatal("merged record was not found")
	}
	if !record.RuntimeKnown || len(record.Runtime) == 0 || record.Name != "x/1" || record.Identity.Scheme != "identity-v1" {
		t.Fatalf("fields were not merged: %#v", record)
	}
}

func TestCacheRejectsChangedFingerprintAndSymlink(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "x.sqf")
	if err := os.WriteFile(image, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := New(func() string { return "" })
	info, _ := os.Lstat(image)
	cache.Merge(image, info, func(record *Record) { record.Name = "x" })
	if err := os.WriteFile(image, []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _ := os.Lstat(image)
	if _, ok := cache.Lookup(image, changed); ok {
		t.Fatal("changed file produced a cache hit")
	}
	link := filepath.Join(dir, "link.sqf")
	if err := os.Symlink(image, link); err != nil {
		t.Fatal(err)
	}
	linkInfo, _ := os.Lstat(link)
	cache.Merge(link, linkInfo, func(record *Record) { record.Name = "link" })
	if _, ok := cache.Lookup(link, linkInfo); ok {
		t.Fatal("symlink produced a cache hit")
	}
}

func TestBatchDefersAndMergesUpdates(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	firstPath := filepath.Join(dir, "first.sqf")
	secondPath := filepath.Join(dir, "second.sqf")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	firstInfo, _ := os.Lstat(firstPath)
	secondInfo, _ := os.Lstat(secondPath)
	cache := New(func() string { return cachePath })
	batch := cache.NewBatch()
	batch.Merge(firstPath, firstInfo, func(record *Record) { record.Name = "first" })
	batch.Merge(secondPath, secondInfo, func(record *Record) { record.Name = "second" })
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("batch wrote before Flush: %v", err)
	}
	batch.Flush()

	reloaded := New(func() string { return cachePath })
	if record, ok := reloaded.Lookup(firstPath, firstInfo); !ok || record.Name != "first" {
		t.Fatalf("first batch record = %#v, %v", record, ok)
	}
	if record, ok := reloaded.Lookup(secondPath, secondInfo); !ok || record.Name != "second" {
		t.Fatalf("second batch record = %#v, %v", record, ok)
	}
}

func TestPruneMissingDropsStaleRecords(t *testing.T) {
	dir := t.TempDir()
	cache := New(func() string { return "" })
	path := filepath.Join(dir, "gone.sqf")
	if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(path)
	cache.Merge(path, info, func(record *Record) { record.Name = "gone" })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := cache.PruneMissing(); got != 1 {
		t.Fatalf("PruneMissing() = %d, want 1", got)
	}
	if _, ok := cache.Lookup(path, info); ok {
		t.Fatal("pruned record remains cached")
	}
}

// otherMount reports a file as seen from a mount with a different device number.
type otherMount struct{ os.FileInfo }

func (o otherMount) Sys() any {
	stat := *o.FileInfo.Sys().(*syscall.Stat_t)
	stat.Dev++
	return &stat
}

func TestFingerprintIgnoresTheDeviceNumber(t *testing.T) {
	image := filepath.Join(t.TempDir(), "x.sqf")
	if err := os.WriteFile(image, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(image)
	cache := New(func() string { return "" })
	cache.Merge(image, info, func(record *Record) { record.Name = "x" })

	if _, ok := cache.Lookup(image, otherMount{info}); !ok {
		t.Fatal("the same file under another device number missed")
	}
}

func TestFileOfAnotherSchemaReadsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	image := filepath.Join(dir, "x.sqf")
	if err := os.WriteFile(image, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(image)
	fp, _ := fingerprint(info)
	old := diskCache{Version: schemaVersion - 1, Records: map[string]Record{image: {Fingerprint: fp, Name: "x"}}}
	if err := utils.WriteGzipJSONFileAtomic(cachePath, old); err != nil {
		t.Fatal(err)
	}

	if _, ok := New(func() string { return cachePath }).Lookup(image, info); ok {
		t.Fatal("a record of another schema was served")
	}
}

// A writer that cannot get the lock does not wait for it: the update stays in
// memory and the file is written by the next writer that can.
func TestWriterDoesNotWaitForAHeldLock(t *testing.T) {
	prev := lockWait
	lockWait = 50 * time.Millisecond
	t.Cleanup(func() { lockWait = prev })

	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	image := filepath.Join(dir, "x.sqf")
	if err := os.WriteFile(image, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(image)

	held, err := os.OpenFile(cachePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	cache := New(func() string { return cachePath })
	start := time.Now()
	cache.Merge(image, info, func(record *Record) { record.Name = "x" })
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Merge waited %v for a held lock", elapsed)
	}
	if record, ok := cache.Lookup(image, info); !ok || record.Name != "x" {
		t.Fatal("the update was not kept in memory")
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("a file was written while the lock was held: %v", err)
	}

	// Having given up, this process does not wait again for the same lock.
	start = time.Now()
	cache.Merge(image, info, func(record *Record) { record.Name = "y" })
	if elapsed := time.Since(start); elapsed > lockWait/2 {
		t.Fatalf("a second write waited %v after the first gave up", elapsed)
	}

	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	other := New(func() string { return cachePath })
	other.Merge(image, info, func(record *Record) { record.Name = "x" })
	if record, ok := New(func() string { return cachePath }).Lookup(image, info); !ok || record.Name != "x" {
		t.Fatal("the record was not written once the lock was free")
	}
}

// Writers that each hold their own copy of the cache, as separate processes do,
// leave every record in the file.
func TestConcurrentWritersKeepEveryRecord(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	const writers = 16
	infos := make([]os.FileInfo, writers)
	paths := make([]string, writers)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("image%d.sqf", i))
		if err := os.WriteFile(paths[i], []byte(paths[i]), 0o644); err != nil {
			t.Fatal(err)
		}
		infos[i], _ = os.Lstat(paths[i])
	}

	var wg sync.WaitGroup
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			New(func() string { return cachePath }).Merge(paths[i], infos[i], func(record *Record) { record.Name = paths[i] })
		}()
	}
	wg.Wait()

	reloaded := New(func() string { return cachePath })
	for i := range paths {
		if record, ok := reloaded.Lookup(paths[i], infos[i]); !ok || record.Name != paths[i] {
			t.Errorf("record %d is missing from the file", i)
		}
	}
}

// A merge that changes no field does not rewrite the file.
func TestUnchangedMergeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	image := filepath.Join(dir, "x.sqf")
	if err := os.WriteFile(image, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(image)
	setName := func(name string) func(*Record) { return func(record *Record) { record.Name = name } }
	inode := func() uint64 {
		stat, err := os.Stat(cachePath)
		if err != nil {
			t.Fatal(err)
		}
		return stat.Sys().(*syscall.Stat_t).Ino
	}

	New(func() string { return cachePath }).Merge(image, info, setName("x"))
	written := inode()
	New(func() string { return cachePath }).Merge(image, info, setName("x"))
	if inode() != written {
		t.Error("a merge that changed nothing rewrote the file")
	}
	New(func() string { return cachePath }).Merge(image, info, setName("y"))
	if inode() == written {
		t.Error("a merge that changed a field did not rewrite the file")
	}
}

func TestUnderRootsAcceptsTheFilesAScanReads(t *testing.T) {
	roots := []string{"/images", "/shared/images/"}
	for path, want := range map[string]bool{
		"/images/star--2.7.sqf":             true,
		"/images/store/star--2.7@abc.sqf":   true,
		"/shared/images/store/x@abc.sqf":    true,
		"/shared/images/x.sqf":              true,
		"/images/sub/x.sqf":                 false,
		"/images/store/deeper/x.sqf":        false,
		"/tmp/x.sqf":                        false,
		"/images.sqf":                       false,
		"/imagesx/x.sqf":                    false,
		"/shared/images/../elsewhere/x.sqf": false,
	} {
		if got := underRoots(roots, path); got != want {
			t.Errorf("underRoots(%q) = %v, want %v", path, got, want)
		}
	}
}

// A cache with a path filter records the accepted paths and drops the others.
func TestCacheRecordsOnlyAcceptedPaths(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, FileName)
	inside := filepath.Join(dir, "images", "a.sqf")
	outside := filepath.Join(dir, "elsewhere", "b.sqf")
	infos := map[string]os.FileInfo{}
	for _, path := range []string{inside, outside} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
		infos[path], _ = os.Lstat(path)
	}
	cache := New(func() string { return cachePath })
	cache.accept = func(path string) bool { return underRoots([]string{filepath.Join(dir, "images")}, path) }
	for path, info := range infos {
		cache.Merge(path, info, func(record *Record) { record.Name = "x" })
	}

	reloaded := New(func() string { return cachePath })
	if _, ok := reloaded.Lookup(inside, infos[inside]); !ok {
		t.Error("a file in the image root was not recorded")
	}
	if _, ok := reloaded.Lookup(outside, infos[outside]); ok {
		t.Error("a file outside the image roots was recorded")
	}
}
