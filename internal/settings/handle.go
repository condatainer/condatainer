package settings

import (
	"fmt"
	"strconv"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// Handle reads one registered key as a Go type. Before any config is loaded it returns the default.
type Handle[T any] struct {
	key  *Key
	conv func(Resolution) T
}

// Get returns the value in effect.
func (h *Handle[T]) Get() T { return h.conv(h.key.resolve()) }

// SetShow sets how the value is displayed. A Show option that reads the handle itself is an
// initialization cycle, so such a key sets it from an init function instead.
func (h *Handle[T]) SetShow(fn func(stored string) string) { h.key.opts.show = fn }

// Key returns the registered key.
func (h *Handle[T]) Key() *Key { return h.key }

func declare(name string, kd kind, opts []Option) *Key {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	k := &Key{Name: name, Help: o.help, Default: o.defaultText, DefaultList: o.defaultList, Order: o.order, kind: kd, opts: o}
	if k.Default != "" {
		stored, err := k.Parse(k.Default)
		if err != nil {
			panic(fmt.Sprintf("settings: default of %q: %v", name, err))
		}
		k.Default = stored
	}
	return register(k)
}

func handle[T any](name string, kd kind, opts []Option, conv func(Resolution) T) *Handle[T] {
	return &Handle[T]{key: declare(name, kd, opts), conv: conv}
}

// Bool declares a boolean key.
func Bool(name string, opts ...Option) *Handle[bool] {
	return handle(name, boolKind, opts, func(r Resolution) bool { return r.Value == "true" })
}

// Int declares a whole-number key.
func Int(name string, opts ...Option) *Handle[int] {
	return handle(name, intKind, opts, func(r Resolution) int { n, _ := strconv.Atoi(r.Value); return n })
}

// Days declares a key holding a whole number of days, read as a duration.
func Days(name string, opts ...Option) *Handle[time.Duration] {
	return handle(name, daysKind, opts, func(r Resolution) time.Duration { return parseDays(r.Value) })
}

// MemoryMB declares a memory size, read in megabytes.
func MemoryMB(name string, opts ...Option) *Handle[int64] {
	return handle(name, memoryKind, opts, func(r Resolution) int64 { mb, _ := utils.ParseMemoryMB(r.Value); return mb })
}

// Walltime declares a time limit, read as a duration.
func Walltime(name string, opts ...Option) *Handle[time.Duration] {
	return handle(name, walltimeKind, opts, func(r Resolution) time.Duration { d, _ := utils.ParseWalltime(r.Value); return d })
}

// Enum declares a key that takes one of Values.
func Enum(name string, opts ...Option) *Handle[string] {
	return handle(name, enumKind, opts, func(r Resolution) string { return r.Value })
}

// Path declares an absolute path, with environment variables expanded on read.
func Path(name string, opts ...Option) *Handle[string] {
	return handle(name, pathKind, opts, func(r Resolution) string { return expandPath(r.Value) })
}

// String declares a free-text key.
func String(name string, opts ...Option) *Handle[string] {
	return handle(name, stringKind, opts, func(r Resolution) string { return r.Value })
}

// List declares a key holding a list of strings.
func List(name string, opts ...Option) *Handle[[]string] {
	return handle(name, listKind, opts, func(r Resolution) []string { return append([]string(nil), r.List...) })
}
