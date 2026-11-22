package ext3

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/toolpath"
)

// Stats holds metadata about the overlay filesystem.
type Stats struct {
	// Filesystem metadata
	FilesystemType  string // "ext3", "ext4"
	BlockSize       int64
	TotalBlocks     int64
	FreeBlocks      int64
	ReservedBlocks  int64
	TotalInodes     int64
	FreeInodes      int64
	FilesystemState string // "clean", "dirty", etc.
	LastMounted     string
	CreatedTime     string
	FilesystemUUID  string
	JournalSize     int64 // in blocks
	InodeSize       int64

	// File metadata
	FileSizeBytes  int64 // Apparent size of the file
	FileBlocksUsed int64 // Actual blocks allocated on disk (for sparse detection)
	IsSparse       bool  // True if file is sparse

	// Overlay ownership
	UpperUID int
	UpperGID int
}

// GetStats reads the filesystem superblock to calculate usage without mounting.
func GetStats(path string) (*Stats, error) {
	if err := tool.CheckDependencies([]string{"tune2fs"}); err != nil {
		return nil, err
	}

	// tune2fs -l lists the superblock info
	tune2fsPath, err := toolpath.Resolve("tune2fs")
	if err != nil {
		return nil, &tool.Error{Op: "read stats", Path: path, Tool: "tune2fs", BaseErr: err}
	}
	cmd := exec.Command(tune2fsPath, "-l", path)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LC_TIME=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, &tool.Error{Op: "read stats", Path: path, Tool: "tune2fs", BaseErr: err}
	}

	stats := &Stats{
		UpperUID: -1,
		UpperGID: -1,
	}
	lines := strings.Split(string(out), "\n")

	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "Filesystem magic number":
			if val == "0xEF53" {
				stats.FilesystemType = "ext3/ext4"
			}
		case "Filesystem features":
			if strings.Contains(val, "has_journal") {
				if strings.Contains(val, "extent") {
					stats.FilesystemType = "ext4"
				} else {
					stats.FilesystemType = "ext3"
				}
			}
		case "Block size":
			stats.BlockSize, _ = strconv.ParseInt(val, 10, 64)
		case "Block count":
			stats.TotalBlocks, _ = strconv.ParseInt(val, 10, 64)
		case "Reserved block count":
			stats.ReservedBlocks, _ = strconv.ParseInt(val, 10, 64)
		case "Free blocks":
			stats.FreeBlocks, _ = strconv.ParseInt(val, 10, 64)
		case "Inode count":
			stats.TotalInodes, _ = strconv.ParseInt(val, 10, 64)
		case "Free inodes":
			stats.FreeInodes, _ = strconv.ParseInt(val, 10, 64)
		case "Inode size":
			stats.InodeSize, _ = strconv.ParseInt(val, 10, 64)
		case "Filesystem state":
			stats.FilesystemState = val
		case "Last mounted on":
			stats.LastMounted = val
		case "Filesystem created":
			stats.CreatedTime = val
		case "Filesystem UUID":
			stats.FilesystemUUID = val
		case "Journal size":
			// Parse journal size (e.g., "32M")
			val = strings.TrimSuffix(val, "M")
			if size, err := strconv.ParseInt(val, 10, 64); err == nil {
				stats.JournalSize = size * 1024 * 1024 / stats.BlockSize // Convert to blocks
			}
		}
	}

	// Get file stats for sparse detection
	if fileInfo, err := os.Stat(path); err == nil {
		stats.FileSizeBytes = fileInfo.Size()

		// Get actual blocks allocated (for sparse detection)
		if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
			stats.FileBlocksUsed = stat.Blocks * 512 // Blocks are in 512-byte units
			// File is sparse if allocated size is significantly less than apparent size
			stats.IsSparse = stats.FileBlocksUsed < stats.FileSizeBytes*9/10
		}
	}

	// Get upper directory ownership using debugfs
	stats.UpperUID, stats.UpperGID = getUpperOwnership(path)

	return stats, nil
}

// getUpperOwnership extracts UID/GID of the upper directory using debugfs.
func getUpperOwnership(path string) (uid, gid int) {
	uid, gid = -1, -1

	debugfsPath, err := toolpath.Resolve("debugfs")
	if err != nil {
		return
	}

	cmd := exec.Command(debugfsPath, "-R", "stat upper", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "User:") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				uid, _ = strconv.Atoi(parts[1])
				gid, _ = strconv.Atoi(parts[3])
			}
			break
		}
	}
	return
}

// Usage returns the calculated used space in bytes and percentage.
func (s *Stats) Usage() (usedBytes int64, percent float64) {
	if s.TotalBlocks == 0 {
		return 0, 0
	}
	usedBlocks := s.TotalBlocks - s.FreeBlocks
	usedBytes = usedBlocks * s.BlockSize
	percent = (float64(usedBlocks) / float64(s.TotalBlocks)) * 100.0
	return
}

// InodeUsage returns the calculated inode usage percentage.
func (s *Stats) InodeUsage() (percent float64) {
	if s.TotalInodes == 0 {
		return 0
	}
	usedInodes := s.TotalInodes - s.FreeInodes
	return (float64(usedInodes) / float64(s.TotalInodes)) * 100.0
}

// ImageOwnerUID reads the owner of the 'upper' directory inside an ext3/4 image, and reports whether it could.
//   - It reads the image with debugfs and never mounts it.
//   - It fails when debugfs is missing, the file is not an ext3/4 image, or the owner is not in the output.
func ImageOwnerUID(imgPath string) (uid int, ok bool) {
	debugfsPath, err := toolpath.Resolve("debugfs")
	if err != nil {
		return 0, false
	}
	output, err := exec.Command(debugfsPath, "-R", "stat upper", imgPath).CombinedOutput()
	if err != nil {
		return 0, false
	}
	// debugfs prints "User:  <uid>   Group:  <gid>".
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "User:") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		uid, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, false
		}
		return uid, true
	}
	return 0, false
}
