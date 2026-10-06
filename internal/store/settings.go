package store

import (
	"time"

	"github.com/condatainer/condatainer/internal/settings"
)

var keyGCGrace = settings.Days("store_gc_grace",
	settings.Default(30), settings.Min(1), settings.Order(6),
	settings.Help("Days below which `store gc` never collects an entry. At least 1."),
	settings.Suggest("7", "30", "90", "180"))

// GCGrace is the age below which `store gc` never reports an entry collectable.
func GCGrace() time.Duration { return keyGCGrace.Get() }
