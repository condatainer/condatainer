package utils

import "bytes"

// StripComments removes every whole-line comment from a recipe, keeping blank lines
// and every other byte. It is the recipe preimage both keys hash: what the recipe does.
//
//   - Annotations are comments, so they disappear too. Each one that reaches a key
//     already has its own record line, and hashing the file would re-admit a #PH:
//     menu that autoupdate grows, a reworded #DESC: or a changed scheduler directive.
//   - The two keys differ in dependencies, not text.
//   - Whole-line only: a trailing comment cannot be removed without parsing shell
//     (`echo "a # b"`, `${v#pre}`). Blank lines are kept, so adding one moves both keys.
func StripComments(text []byte) []byte {
	out := make([]byte, 0, len(text))
	for off := 0; off < len(text); {
		next := len(text)
		line := text[off:]
		if end := bytes.IndexByte(line, '\n'); end >= 0 {
			line, next = line[:end+1], off+end+1
		}
		content := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if trimmed := bytes.TrimLeft(content, " \t"); len(trimmed) == 0 || trimmed[0] != '#' {
			out = append(out, line...)
		}
		off = next
	}
	return out
}
