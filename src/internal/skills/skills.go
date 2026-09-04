// Package skills carries the documents Luna can install into an agent's skill
// directory, so a session knows how to call the tool without being told.
//
// Embedded rather than shipped alongside: a skill that documents `luna report`
// against a binary that answers `luna runs` costs somebody a session, and the
// two drift the moment they live in different places. The version that installs
// is the version that was built.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed all:luna
var files embed.FS

// Skill is one installable document tree.
type Skill struct {
	// Name is the directory it installs as, and the name an agent invokes.
	Name string

	// Files are its contents, keyed by path relative to the skill's own root.
	// Relative because the installer joins them onto a destination, and a
	// leading "luna/" would nest the skill inside itself.
	Files map[string][]byte
}

// All returns every skill this build carries, in a stable order.
//
// Plural from the start: this one is about the tool itself, and the next is more
// likely than not — a set that has to be reshaped to hold a second thing usually
// gets copied instead.
//
// No error to return. The tree is fixed when the binary is linked, so there is
// nothing here that can fail at runtime and no way to test a branch that says it
// did. What can go wrong is an embed pattern that matched nothing, and that is
// caught by a test asserting the skill is present.
func All() []Skill {
	var all []Skill
	for _, entry := range must(fs.ReadDir(files, ".")) {
		if entry.IsDir() {
			all = append(all, load(entry.Name()))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

// Find answers with one skill by name, naming what is carried when it cannot.
func Find(name string) (Skill, error) {
	all := All()
	for _, skill := range all {
		if skill.Name == name {
			return skill, nil
		}
	}

	names := make([]string, 0, len(all))
	for _, skill := range all {
		names = append(names, skill.Name)
	}
	return Skill{}, fmt.Errorf("no skill named %q — this build carries %s",
		name, strings.Join(names, ", "))
}

func load(name string) Skill {
	skill := Skill{Name: name, Files: map[string][]byte{}}
	_ = fs.WalkDir(files, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// embed.FS paths are always slash-separated, whatever the platform, so
		// the prefix comes off with a string trim rather than filepath.
		skill.Files[strings.TrimPrefix(p, name+"/")] = must(files.ReadFile(p))
		return nil
	})
	return skill
}

// must unwraps a read of the embedded tree, which cannot fail: the bytes were
// linked into this binary. A panic here means the build is broken, and that is
// worth saying loudly rather than threading an impossible error upwards.
func must[T any](value T, err error) T {
	if err != nil {
		panic("the embedded skills are unreadable, which means this binary is corrupt: " + err.Error())
	}
	return value
}
