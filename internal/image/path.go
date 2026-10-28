package image

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/image/squashfs"
	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/toolpath"
	"github.com/condatainer/condatainer/internal/utils"
)

// imgPathExists checks whether entry exists inside an ext3 image.
// It uses debugfs to stat the upper/<entry> path in the image.
func imgPathExists(imgPath, entry string) bool {
	entry = strings.TrimPrefix(entry, "/")
	dbg, err := toolpath.Resolve("debugfs")
	if err != nil {
		return false
	}
	statArg := "stat upper/" + entry
	cmd := exec.Command(dbg, "-R", statArg, imgPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "File not found") || strings.Contains(outStr, "No such file") {
			return false
		}
		return false
	}
	return true
}

// PathExists dispatches to the appropriate backend check depending on the image type.
func PathExists(imagePath, entry string) bool {
	if utils.IsSqf(imagePath) {
		return squashfs.PathExists(imagePath, entry)
	}
	if utils.IsImg(imagePath) {
		return imgPathExists(imagePath, entry)
	}
	return false
}

// imgTopLevel lists the names directly under upper/ in an ext3 image.
func imgTopLevel(imgPath string) ([]string, error) {
	dbg, err := toolpath.Resolve("debugfs")
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(dbg, "-R", "ls -p upper", imgPath).Output()
	if err != nil {
		return nil, &tool.Error{Op: "list", Path: imgPath, Tool: "debugfs", BaseErr: err}
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		// "/inode/mode/uid/gid/name/size/"
		fields := strings.Split(strings.TrimSpace(line), "/")
		if len(fields) < 6 || fields[5] == "." || fields[5] == ".." || fields[5] == "" {
			continue
		}
		names = append(names, fields[5])
	}
	slices.Sort(names)
	return names, nil
}

// TopLevel lists the names at the root of an image's payload: the archive root
// of a .sqf, and upper/ of an .img.
func TopLevel(imagePath string) ([]string, error) {
	switch {
	case utils.IsSqf(imagePath):
		return squashfs.TopLevel(imagePath)
	case utils.IsImg(imagePath):
		return imgTopLevel(imagePath)
	}
	return nil, fmt.Errorf("%s is not a SquashFS or ext3 image", imagePath)
}
