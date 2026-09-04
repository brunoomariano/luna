package skills_test

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/skills"
)

// The skills are embedded, so "is it there" is a build-time question that only a
// test asks out loud: a mistyped //go:embed pattern compiles and ships nothing.
func TestThisBuildCarriesTheLunaSkill(t *testing.T) {
	all := skills.All()
	if len(all) == 0 {
		t.Fatal("this build carries no skills; the embed pattern matched nothing")
	}

	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	body, carried := skill.Files["SKILL.md"]
	if !carried {
		t.Fatalf("the luna skill has no SKILL.md, only %v", names(skill))
	}
	if len(body) < 1000 {
		t.Errorf("SKILL.md is %d bytes, which is too short to be the document", len(body))
	}
	if !strings.HasPrefix(string(body), "---\nname: luna\n") {
		t.Error("SKILL.md does not open with the frontmatter a harness reads")
	}
}

// Asking for something that is not carried has to say what is, or the caller is
// left guessing at a name.
func TestAnUnknownSkillNamesWhatIsCarried(t *testing.T) {
	_, err := skills.Find("a-skill-this-build-does-not-have")
	if err == nil {
		t.Fatal("an unknown skill was found")
	}
	if !strings.Contains(err.Error(), "luna") {
		t.Errorf("the refusal does not say what is carried: %v", err)
	}
}

// Files are keyed relative to the skill's own root: the installer joins them
// onto a destination, and a leading "luna/" would nest it twice.
func TestFilesAreKeyedRelativeToTheSkillRoot(t *testing.T) {
	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	for name := range skill.Files {
		if strings.HasPrefix(name, "luna/") || strings.HasPrefix(name, "/") {
			t.Errorf("%q is not relative to the skill's root", name)
		}
	}
}

func names(skill skills.Skill) []string {
	out := make([]string, 0, len(skill.Files))
	for name := range skill.Files {
		out = append(out, name)
	}
	return out
}
