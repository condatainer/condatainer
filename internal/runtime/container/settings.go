package container

import (
	"strings"

	"github.com/condatainer/condatainer/internal/settings"
)

var (
	keyAutoloadGPU = settings.Bool("autoload_gpu",
		settings.Default(true), settings.Order(3),
		settings.Help("Pass --nv and --rocm to the container when the host has a GPU."))

	keyBind = settings.List("bind",
		settings.MergeLists(), settings.Order(8),
		settings.Split(func(text string) []string {
			var out []string
			for _, b := range strings.Split(text, "|") {
				if b = strings.TrimSpace(b); b != "" {
					out = append(out, b)
				}
			}
			return out
		}),
		settings.Help("Paths added to every container, as host[:container[:opts]]. Every layer's list is merged."))
)

// AutoloadGPU reports whether GPU flags are added without being asked for.
func AutoloadGPU() bool { return keyAutoloadGPU.Get() }

// Binds returns the configured binds, merged across layers.
func Binds() []string { return keyBind.Get() }
