package khsier_test

// This file guards the thin release caller's Homebrew automation contract.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestHomebrewReleaseWorkflowContract(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	job := regexp.MustCompile(`(?ms)^  homebrew:\n(.*?)(?:^  [a-z][a-z_-]*:\n|\z)`).FindStringSubmatch(string(data))
	if job == nil {
		t.Fatal("release workflow must call the common Homebrew workflow")
	}
	for _, required := range []string{
		"    if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && needs.prepare.outputs.homebrew-eligible == 'true'\n",
		"    needs:\n      - prepare\n      - release\n",
		"    permissions:\n      contents: read\n",
		"      formula: khsier\n",
		"      tag: ${{ needs.prepare.outputs.version }}\n",
		"      app-id: ${{ vars.HOMEBREW_TAP_APP_ID }}\n",
		"    secrets:\n      app-private-key: ${{ secrets.HOMEBREW_TAP_APP_PRIVATE_KEY }}\n",
	} {
		if !strings.Contains(job[1], required) {
			t.Errorf("Homebrew caller missing contract: %q", required)
		}
	}
	pin := regexp.MustCompile(`(?m)^    uses: zaubermaerchen/homebrew-tap/\.github/workflows/update-formula\.yml@(main|[a-f0-9]{40})$`).FindStringSubmatch(job[1])
	if pin == nil {
		t.Fatal("Homebrew caller must use the tap's common workflow")
	}
	if !strings.Contains(job[1], "      automation-ref: "+pin[1]+"\n") {
		t.Error("common workflow and automation checkout must use the same revision")
	}
	for _, forbidden := range []string{"always()", "contents: write", "pull-requests: write", "    steps:", "    runs-on:", "secrets: inherit"} {
		if strings.Contains(job[1], forbidden) {
			t.Errorf("Homebrew caller must not include %q", forbidden)
		}
	}
}

func TestReleasePrepareHomebrewEligibility(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release preparation runs Bash on Ubuntu")
	}
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "      homebrew-eligible: ${{ steps.version.outputs.homebrew-eligible }}\n") {
		t.Error("prepare must expose Homebrew eligibility to downstream jobs")
	}
	script := regexp.MustCompile(`(?ms)^        run: \|\n(.*?)(?:\n  test:\n)`).FindStringSubmatch(string(data))
	if script == nil {
		t.Fatal("release preparation script not found")
	}
	for _, tc := range []struct {
		tag, event, version, eligible string
	}{
		{"v1.2.3", "push", "v1.2.3", "true"},
		{"v1.2.3-alpha", "push", "v1.2.3-alpha", "false"},
		{"v1.2.3+build", "push", "v1.2.3+build", "false"},
		{"v1.2.3", "pull_request", "snapshot", "false"},
	} {
		t.Run(tc.event+"/"+tc.tag, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), "output")
			command := exec.Command("bash", "-c", script[1])
			command.Env = append(os.Environ(), "GITHUB_EVENT_NAME="+tc.event, "GITHUB_REF_NAME="+tc.tag, "GITHUB_OUTPUT="+outputPath)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("prepare script failed: %v\n%s", err, output)
			}
			output, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			want := "version=" + tc.version + "\nhomebrew-eligible=" + tc.eligible + "\n"
			if string(output) != want {
				t.Fatalf("prepare output = %q, want %q", output, want)
			}
		})
	}
}
