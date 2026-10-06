package build

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
)

const (
	// Data is large and read sequentially, so it takes the bigger block; an app
	// is many small files where a big block wastes read bandwidth.
	defaultBlockSize     = "128k"
	defaultDataBlockSize = "512k"
)

var (
	keyLogsDir = settings.Path("build.logs_dir",
		settings.DefaultFunc(func() any { return DefaultLogsDir() }),
		settings.Order(2),
		settings.Help("Where build and restore job logs go. $SCRATCH/logs when SCRATCH is set, else $HOME/logs."))

	keyAlwaysSubmitData = settings.Bool("build.always_submit_data",
		settings.Default(false), settings.Order(3),
		settings.Help("Submit data builds as scheduler jobs even when the recipe has no scheduler directives."))

	keyNcpus = settings.Int("build.ncpus",
		settings.Default(config.DefaultNcpus), settings.Min(1), settings.Order(4),
		settings.Help("CPUs for a build job."),
		settings.Suggest("4", "8", "16", "32"))

	keyMem = settings.MemoryMB("build.mem",
		settings.Default(12288), settings.Order(5),
		settings.Help("Memory for a build job."),
		settings.Suggest("4g", "8g", "16g", "32g"))

	keyTime = settings.Walltime("build.time",
		settings.Default("2h"), settings.Order(6),
		settings.Help("Time limit for a build job."),
		settings.Suggest("1h", "2h", "4h", "8h"))

	keyCompressArgs = settings.String("build.compress_args",
		settings.Default("zstd-medium"), settings.Order(7),
		settings.Normalize(ArgsForCompress),
		settings.Accepts("a preset ("+strings.Join(CompressNames(), ", ")+") or mksquashfs arguments"),
		settings.Help("mksquashfs compression arguments, or a preset name such as zstd-medium."),
		settings.Suggest(CompressNames()...))

	keyBlockSize = settings.String("build.block_size",
		settings.Default(defaultBlockSize), settings.Order(8),
		settings.Validate(validateBlockSize), settings.Accepts(blockSizeAccepts),
		settings.Help("SquashFS block size of every overlay except data."),
		settings.Suggest(BlockSizeCompletions...))

	keyDataBlockSize = settings.String("build.data_block_size",
		settings.Default(defaultDataBlockSize), settings.Order(9),
		settings.Validate(validateBlockSize), settings.Accepts(blockSizeAccepts),
		settings.Help("SquashFS block size of data overlays."),
		settings.Suggest(BlockSizeCompletions...))
)

// CompressArgs is the mksquashfs compression argument string.
func CompressArgs() string { return keyCompressArgs.Get() }

// BlockSize is the SquashFS block size of every overlay except data.
func BlockSize() string { return keyBlockSize.Get() }

// DataBlockSize is the SquashFS block size of data overlays.
func DataBlockSize() string { return keyDataBlockSize.Get() }

// AlwaysSubmitData reports whether data builds are submitted even without scheduler directives.
func AlwaysSubmitData() bool { return keyAlwaysSubmitData.Get() }

// LogsDir is where build and restore job logs go.
func LogsDir() string { return keyLogsDir.Get() }

// DefaultSpec is the resource request of a build job.
func DefaultSpec() scheduler.ResourceSpec {
	return scheduler.ResourceSpec{CpusPerTask: keyNcpus.Get(), MemPerNodeMB: keyMem.Get(), Time: keyTime.Get()}
}

// DefaultLogsDir is where build and restore job logs land when build.logs_dir is not set: $SCRATCH/logs when SCRATCH is set, else $HOME/logs.
func DefaultLogsDir() string {
	if scratch := os.Getenv("SCRATCH"); scratch != "" {
		return filepath.Join(scratch, "logs")
	}
	return filepath.Join(os.Getenv("HOME"), "logs")
}

// BlockSizeCompletions lists common mksquashfs block sizes for shell completion
var BlockSizeCompletions = []string{"64k", "128k", "256k", "512k", "1m"}

// IsValidBlockSize validates a mksquashfs -b value.
// Must be a power of two between 4096 and 1048576 (1M), with optional k/K or m/M suffix.
func IsValidBlockSize(size string) bool {
	if size == "" {
		return false
	}
	s := strings.ToLower(size)
	var multiplier int64 = 1
	if strings.HasSuffix(s, "k") {
		multiplier = 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return false
	}
	bytes := n * multiplier
	// mksquashfs requires power of two, between 4096 and 1048576 (1M)
	return bytes >= 4096 && bytes <= 1048576 && (bytes&(bytes-1)) == 0
}

func validateBlockSize(size string) error {
	if !IsValidBlockSize(size) {
		return errBlockSize
	}
	return nil
}

const blockSizeAccepts = "a power of two from 4k to 1m, such as 128k or 1m"

var errBlockSize = errors.New("must be a power of two between 4096 and 1M, such as 64k, 128k, 512k or 1m")
