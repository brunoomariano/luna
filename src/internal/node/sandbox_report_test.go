package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheReportCarriesWhatTheConfigurationSays covers the ordinary case: both
// files present, and what they hold is what the gate shows.
func TestTheReportCarriesWhatTheConfigurationSays(t *testing.T) {
	repo := t.TempDir()
	writeConfigFile(t, repo, jailConfig, "rw_maps = [\"~/repos/personal/luna\"]\nnetwork = true\n")
	writeConfigFile(t, repo, memoryConfig, "workspace = \"personal\"\nproject = \"luna\"\n")

	report := SandboxReport(repo)

	for _, want := range []string{"rw_maps", "personal", jailConfig, memoryConfig} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not carry %q:\n%s", want, report)
		}
	}
}

// TestAMissingFileIsNamedRatherThanFilledIn is the difference between a report
// and a repair.
//
// A default written into the repository is a change nobody asked for, and writing
// it before a person has seen what is there is the opposite of what the gate is
// for. Saying "not present" is what lets them decide.
func TestAMissingFileIsNamedRatherThanFilledIn(t *testing.T) {
	repo := t.TempDir()

	report := SandboxReport(repo)

	if !strings.Contains(report, "Not present") {
		t.Errorf("a missing file must be named as missing:\n%s", report)
	}
	if _, err := os.Stat(filepath.Join(repo, jailConfig)); err == nil {
		t.Error("the report wrote a configuration file into the repository")
	}
}

// TestAnEmptyFileIsNotTheSameAsAMissingOne. Both are answers to the gate's
// question and they are opposite ones: a file that is there and empty was
// written, and one that is absent was never made.
func TestAnEmptyFileIsNotTheSameAsAMissingOne(t *testing.T) {
	repo := t.TempDir()
	writeConfigFile(t, repo, jailConfig, "")

	report := SandboxReport(repo)

	jail := strings.Index(report, "## "+jailConfig)
	memory := strings.Index(report, "## "+memoryConfig)
	if jail < 0 || memory < 0 {
		t.Fatalf("both files must be reported on:\n%s", report)
	}
	if strings.Contains(report[jail:memory], "Not present") {
		t.Errorf("an empty file was reported as missing:\n%s", report[jail:memory])
	}
}

func writeConfigFile(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}
