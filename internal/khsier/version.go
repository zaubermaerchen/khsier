package khsier

// This file resolves the build version and renders khsier's version output.

import (
	"fmt"
	"io"
	"regexp"
	"runtime/debug"
)

// version is populated for release builds with -ldflags -X github.com/zaubermaerchen/khsier/internal/khsier.version=....
var version string

var goPseudoVersionPattern = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func resolveVersion(linkerVersion string, buildInfo *debug.BuildInfo, buildInfoOK bool) string {
	if linkerVersion != "" {
		return linkerVersion
	}
	if buildInfoOK && buildInfo != nil && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" && !isVCSModified(buildInfo) && !isPseudoVersion(buildInfo.Main.Version) {
		return buildInfo.Main.Version
	}
	return "devel"
}

func isPseudoVersion(value string) bool {
	return goPseudoVersionPattern.MatchString(value)
}

func isVCSModified(buildInfo *debug.BuildInfo) bool {
	if buildInfo == nil {
		return false
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return true
		}
	}
	return false
}

func currentVersion() string {
	buildInfo, ok := debug.ReadBuildInfo()
	return resolveVersion(version, buildInfo, ok)
}

func printVersion(out io.Writer) {
	_, _ = fmt.Fprintf(out, "khsier %s\n", currentVersion())
}
