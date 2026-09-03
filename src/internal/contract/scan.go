package contract

import (
	"fmt"
	"strings"
)

// The TOML this reads is a small, closed subset: sections, string keys, whole
// numbers and lists of strings. It is hand-written rather than a dependency
// because that subset is a few dozen lines and Luna ships with no dependencies —
// a contract format that pulls in a parser is a contract format that pulls in a
// supply chain.

// sectionName reads a `[section]` header.
func sectionName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	return strings.TrimSpace(line[1 : len(line)-1]), true
}

// parseStrings reads a `["a", "b"]` list.
func parseStrings(value, at string) ([]string, error) {
	inner, ok := strings.CutPrefix(strings.TrimSpace(value), "[")
	if !ok {
		return nil, fmt.Errorf("%s: expected a list like [\"a\", \"b\"], got %q", at, value)
	}
	inner, ok = strings.CutSuffix(strings.TrimSpace(inner), "]")
	if !ok {
		return nil, fmt.Errorf("%s: a list that opens with [ has to close with ]", at)
	}

	var list []string
	for _, item := range strings.Split(inner, ",") {
		item = unquote(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		list = append(list, item)
	}
	return list, nil
}

// readLine returns the next logical line and how many extra physical ones it
// swallowed.
//
// The two differ only for a list that spans lines, which is why this exists
// rather than being inlined: a contract's `converges_on` and `requires` are read
// by people at a gate, and forcing them onto one line makes the file unreadable
// exactly where it most needs reading.
func readLine(lines []string, number int, where string) (line string, consumed int, err error) {
	line = strings.TrimSpace(stripComment(lines[number]))
	if line == "" {
		return "", 0, nil
	}

	joined, consumed, err := joinList(lines, number, where)
	if err != nil {
		return "", 0, err
	}
	if consumed > 0 {
		return joined, consumed, nil
	}
	return line, 0, nil
}

// joinList gathers a list that opens on one line and closes on a later one.
//
// An unclosed list that reaches the end of the file is an error rather than a
// silently truncated one: a `converges_on` missing its ] would otherwise load
// with however many entries happened to precede the mistake, and a floor with
// half its entries is a floor that passes when it should hold.
func joinList(lines []string, start int, where string) (line string, consumed int, err error) {
	first := strings.TrimSpace(stripComment(lines[start]))

	_, value, found := strings.Cut(first, "=")
	if !found {
		return "", 0, nil
	}
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || strings.Contains(value, "]") {
		return "", 0, nil
	}

	var b strings.Builder
	b.WriteString(first)
	for i := start + 1; i < len(lines); i++ {
		next := strings.TrimSpace(stripComment(lines[i]))
		b.WriteString(" ")
		b.WriteString(next)

		if strings.Contains(next, "]") {
			return b.String(), i - start, nil
		}
	}

	return "", 0, fmt.Errorf("%s:%d: a list that opens with [ has to close with ]", where, start+1)
}

// unquote strips the quotes TOML puts around a string.
func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}

// stripComment removes a trailing `#` comment.
//
// It respects quotes, because a command is a string and `git log --format=%h #`
// is a legitimate thing to write. Naive splitting on `#` would silently truncate
// the command and leave a verifier proving something other than what was
// declared.
func stripComment(line string) string {
	var quoted bool
	for i, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
	}
	return line
}
