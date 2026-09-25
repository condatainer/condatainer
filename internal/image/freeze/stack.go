package freeze

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/logging"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/toolpath"
)

// archiveNode is one entry of an archive listing.
type archiveNode struct {
	Path         string
	Kind         byte // '-' file, 'd' directory, 'l' symlink, 'c' char device
	Major, Minor int
	Size         int64
}

// IsWhiteout reports a char 0:0 node, which is how an archive records a deletion.
func (n archiveNode) IsWhiteout() bool { return n.Kind == 'c' && n.Major == 0 && n.Minor == 0 }

var listingLine = regexp.MustCompile(`^(\S+)\s+\S+\s+(?:(\d+),\s*(\d+)|(\d+))\s+\d{4}-\d{2}-\d{2} \d{2}:\d{2}\s+(.*)$`)

// readArchive lists a SquashFS archive with unsquashfs, without mounting it.
func readArchive(ctx context.Context, sqf string) ([]archiveNode, error) {
	bin, err := toolpath.Resolve("unsquashfs")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "-ll", "-no-progress", sqf)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list %s: %w: %s", sqf, err, strings.TrimSpace(stderr.String()))
	}
	var nodes []archiveNode
	for _, line := range strings.Split(stdout.String(), "\n") {
		m := listingLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[5]
		if i := strings.Index(name, " -> "); i >= 0 {
			name = name[:i]
		}
		p, ok := strings.CutPrefix(name, "squashfs-root/")
		if !ok {
			continue
		}
		n := archiveNode{Path: p, Kind: m[1][0]}
		if m[2] != "" {
			n.Major, _ = strconv.Atoi(m[2])
			n.Minor, _ = strconv.Atoi(m[3])
		} else {
			n.Size, _ = strconv.ParseInt(m[4], 10, 64)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// stackedPlan is what layering the image over its snapshot changes about the pack.
type stackedPlan struct {
	// Translation is the deletions to inject, from the image and inherited from
	// the snapshot. A union mount swallows every char 0:0 node, so none survive
	// into the packed view on their own.
	Translation Translation
	// Sources are the archive roots that may exist in the merged view.
	Sources []string
}

// planStacked works out the deletions and roots for packing the image over the
// snapshot listed in snapshotNodes.
//   - The image's own deletions are injected as the copy route injects them, since a union swallows the char 0:0 nodes it holds.
//   - The snapshot's own whiteouts are injected too, unless the image recreates the path or hides its parent.
//   - The snapshot's own metadata directory is not a root. The new archive gets its own.
//   - A whiteout is injected only where its parent exists in the merged view, since mksquashfs cannot define a node under a missing directory.
func planStacked(snapshotNodes []archiveNode, entries []Entry, tr Translation) stackedPlan {
	out := tr.ForCopy()
	out.Exclude = nil

	deleted := map[string]bool{}
	for _, line := range out.Pseudo {
		deleted[strings.Fields(line)[0]] = true
	}
	provided := map[string]Entry{}
	for _, e := range entries {
		if n := e.Name(); n == opqMarker || strings.HasPrefix(n, whPrefix) || e.IsCharDev() {
			continue
		}
		provided[e.Path] = e
	}
	opaque := map[string]bool{}
	for _, d := range tr.Opaque {
		opaque[d] = true
	}

	for _, n := range snapshotNodes {
		if !n.IsWhiteout() || deleted[n.Path] {
			continue
		}
		if _, recreated := provided[n.Path]; recreated {
			continue
		}
		if !parentVisible(n.Path, provided, deleted, opaque) {
			continue
		}
		deleted[n.Path] = true
		out.Pseudo = append(out.Pseudo, pseudoWhiteout(n.Path))
	}
	sort.Strings(out.Pseudo)

	roots := map[string]bool{}
	for _, r := range archiveSources(entries, tr) {
		roots[r] = true
	}
	for _, n := range snapshotNodes {
		if !strings.Contains(n.Path, "/") && !n.IsWhiteout() && n.Path != meta.DirName {
			roots[n.Path] = true
		}
	}
	sources := make([]string, 0, len(roots))
	for r := range roots {
		sources = append(sources, r)
	}
	sort.Strings(sources)
	return stackedPlan{Translation: out, Sources: sources}
}

// parentVisible reports whether every directory above p exists in the merged view.
//   - A directory the image deleted, replaced with a file, or left opaque without recreating a level hides what the snapshot holds beneath it.
func parentVisible(p string, provided map[string]Entry, deleted, opaque map[string]bool) bool {
	parts := strings.Split(p, "/")
	hidden := false
	for i := 1; i < len(parts); i++ {
		q := path.Join(parts[:i]...)
		if deleted[q] {
			return false
		}
		if e, ok := provided[q]; ok {
			if !e.IsDir() {
				return false
			}
			hidden = opaque[q]
			continue
		}
		if hidden {
			return false
		}
	}
	return true
}

// packStacked packs the image layered over its snapshot into one self-contained
// archive, reading both through mounts in a private unprivileged namespace.
//   - The snapshot and the image are lower layers of a fuse-overlayfs union with a scratch upper, so neither is written.
//   - The union applies the image's edits, deletions, opaque directories and type changes as they mount.
//   - Deletions are injected as pseudo-file whiteouts, because the union swallows char 0:0 nodes.
func packStacked(ctx context.Context, opts PackOptions, img, target, stage, mksquashfsBin string,
	tools meta.BuildTools, entries []Entry, tr Translation) (meta.BuildTools, error) {
	log := logging.FromContext(ctx)

	fuse2fs, err := findFuse2fs()
	if err != nil {
		return meta.BuildTools{}, err
	}
	squashfuse, err := FindSquashfuse()
	if err != nil {
		return meta.BuildTools{}, err
	}
	overlayfs, err := FindFuseOverlayfs()
	if err != nil {
		return meta.BuildTools{}, err
	}
	tools.Fuse2fs = fuse2fsVersion(ctx, fuse2fs)
	tools.FuseOverlayfs = fuseOverlayfsVersion(ctx, overlayfs)

	snapshot, err := filepath.Abs(opts.Snapshot)
	if err != nil {
		return meta.BuildTools{}, err
	}
	nodes, err := readArchive(ctx, snapshot)
	if err != nil {
		return meta.BuildTools{}, err
	}
	plan := planStacked(nodes, entries, tr)
	if len(plan.Sources) == 0 {
		return meta.BuildTools{}, fmt.Errorf("%s and %s hold nothing to freeze", opts.Image, opts.Snapshot)
	}

	restore, err := protect(img)
	if err != nil {
		return meta.BuildTools{}, err
	}
	defer restore()

	dirs := map[string]string{}
	for _, name := range []string{"mnt", "lower", "upper", "work", "merged"} {
		dirs[name] = filepath.Join(stage, name)
		if err := os.MkdirAll(dirs[name], 0o755); err != nil {
			return meta.BuildTools{}, fmt.Errorf("stage %s: %w", name, err)
		}
	}
	args, err := stageTranslation(stage, "", plan.Translation)
	if err != nil {
		return meta.BuildTools{}, err
	}
	script := stackedScript(stackedRun{
		Snapshot: snapshot, Squashfuse: squashfuse, Overlayfs: overlayfs,
		ImageUpper: filepath.Join(dirs["mnt"], UpperDir),
		Lower:      dirs["lower"], Upper: dirs["upper"], Work: dirs["work"], Merged: dirs["merged"],
		Sources: plan.Sources,
	}, packScript([]string{`"${srcs[@]}"`}, target, args, opts, mksquashfsBin))

	log.Info("packing frozen environment", "source", opts.Image, "target", opts.Target,
		"route", "stacked on "+opts.Snapshot, "roots", strings.Join(plan.Sources, " "),
		"deletions", len(plan.Translation.Pseudo))
	err = MountedRun(ctx, fuse2fs, []string{"-o", "ro", img}, dirs["mnt"], script, execpkg.IOFromContext(ctx))
	if err != nil {
		os.Remove(target)
		return meta.BuildTools{}, fmt.Errorf("pack %s: %w", opts.Target, err)
	}
	if _, err := os.Stat(target); err != nil {
		return meta.BuildTools{}, fmt.Errorf("pack produced no artifact at %s: %w", opts.Target, err)
	}
	return tools, nil
}

// stackedRun is what the script that layers the mounts needs.
type stackedRun struct {
	Snapshot, Squashfuse, Overlayfs string
	ImageUpper                      string
	Lower, Upper, Work, Merged      string
	Sources                         []string
}

// stackedScript renders the work run inside the image's mount: mount the
// snapshot, union it under the image, and pack the roots that exist in the union.
//   - The FUSE processes stay in the background, so they are killed on exit. Otherwise the namespace would never end.
//   - ls tests for a root, not a stat of a device node through a mount, which can hang.
func stackedScript(r stackedRun, pack string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	fmt.Fprintf(&b, "waitmnt() { for _ in $(seq 100); do grep -q \" $1 \" /proc/self/mountinfo && return 0; sleep 0.1; done; echo \"mount of $1 did not appear\" >&2; return 1; }\n")
	b.WriteString("trap 'kill $P1 $P2 2>/dev/null || true' EXIT\n")
	fmt.Fprintf(&b, "%s -f %s %s &\nP1=$!\nwaitmnt %s\n",
		shellQuote(r.Squashfuse), shellQuote(r.Snapshot), shellQuote(r.Lower), shellQuote(r.Lower))
	fmt.Fprintf(&b, "%s -f -o %s %s &\nP2=$!\nwaitmnt %s\n",
		shellQuote(r.Overlayfs),
		shellQuote("lowerdir="+r.ImageUpper+":"+r.Lower+",upperdir="+r.Upper+",workdir="+r.Work),
		shellQuote(r.Merged), shellQuote(r.Merged))
	fmt.Fprintf(&b, "cd %s\nsrcs=()\n", shellQuote(r.Merged))
	for _, s := range r.Sources {
		fmt.Fprintf(&b, "ls -d -- %s >/dev/null 2>&1 && srcs+=(%s)\n", shellQuote(s), shellQuote(s))
	}
	b.WriteString(pack)
	return b.String()
}

// payloadFacts is what the manifest records about the payload.
type payloadFacts struct {
	MB, Entries, Whiteouts int
	Roots                  []string
}

// archiveFacts reads the payload's totals off a finished archive, for an image
// packed over a snapshot, whose entries the image alone does not list.
func archiveFacts(ctx context.Context, sqf string) (payloadFacts, error) {
	nodes, err := readArchive(ctx, sqf)
	if err != nil {
		return payloadFacts{}, err
	}
	const block = 4096
	var used int64
	f := payloadFacts{Entries: len(nodes)}
	for _, n := range nodes {
		if n.Size > block {
			used += (n.Size + block - 1) / block * block
		} else {
			used += block
		}
		if n.IsWhiteout() {
			f.Whiteouts++
		}
		if !strings.Contains(n.Path, "/") {
			f.Roots = append(f.Roots, n.Path)
		}
	}
	f.MB = int(used/(1024*1024)) + 1
	return f, nil
}
