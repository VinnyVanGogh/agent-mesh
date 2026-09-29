package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// VersionInfo is the result of probing a provider CLI with `--version`.
type VersionInfo struct {
	Provider string
	Bin      string
	Raw      string
	Major    int
	Minor    int
	Patch    int
	// Known is true when Major is in the adapter's KnownMajorVersions.
	Known bool
	// Warning is non-empty when the version is unknown or unparseable.
	Warning string
}

func (v VersionInfo) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

var semverRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

const versionProbeTimeout = 10 * time.Second

// ProbeVersion runs `<bin> --version` and checks the major version against the
// adapter's verified set. A non-nil error means the CLI could not be run at all;
// an unknown or unparseable version is reported via VersionInfo.Warning instead.
func ProbeVersion(ctx context.Context, a ProviderAdapter, bin string) (VersionInfo, error) {
	info := VersionInfo{Provider: a.Provider(), Bin: bin}

	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return info, fmt.Errorf("%s --version: %w", bin, err)
	}
	info.Raw = strings.TrimSpace(out.String())
	return checkVersion(a, info), nil
}

func checkVersion(a ProviderAdapter, info VersionInfo) VersionInfo {
	m := semverRe.FindStringSubmatch(info.Raw)
	if m == nil {
		info.Warning = fmt.Sprintf("%s CLI version could not be parsed from %q; flags and stream schema are unverified", a.Provider(), firstLine(info.Raw))
		return info
	}
	info.Major, _ = strconv.Atoi(m[1])
	info.Minor, _ = strconv.Atoi(m[2])
	info.Patch, _ = strconv.Atoi(m[3])

	known := a.KnownMajorVersions()
	for _, k := range known {
		if k == info.Major {
			info.Known = true
			return info
		}
	}
	info.Warning = fmt.Sprintf("%s CLI %s has unknown major version %d (verified: %v); flags or stream schema may have drifted",
		a.Provider(), info, info.Major, known)
	return info
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
