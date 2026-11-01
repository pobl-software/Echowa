package services

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/host"

	"goravel/app/release"
)

// ServerFacts is what the dashboard says about the machine itself, beside
// its figures. A fact the host won't give is empty. Mirrored by ServerFacts
// in resources/js/types/models.ts.
type ServerFacts struct {
	Hostname string `json:"hostname"`
	// OS is the distribution and its version, "Ubuntu 24.04".
	OS       string     `json:"os"`
	Kernel   string     `json:"kernel"`
	BootedAt *time.Time `json:"booted_at"`
	// Version is Echowa's own, and Update a newer release's, from the last
	// look at the channel; empty when there's none to install. Checked is
	// whether the last look worked: a dev build never looks.
	Version string `json:"version"`
	Update  string `json:"update"`
	Checked bool   `json:"update_checked"`
}

func CurrentServerFacts(ctx context.Context) ServerFacts {
	facts := ServerFacts{Version: release.Version}
	facts.Hostname, _ = os.Hostname()
	if platform, _, version, err := host.PlatformInformationWithContext(ctx); err == nil && platform != "" {
		facts.OS = strings.TrimSpace(strings.ToUpper(platform[:1]) + platform[1:] + " " + version)
	}
	facts.Kernel, _ = host.KernelVersionWithContext(ctx)
	if boot, err := host.BootTimeWithContext(ctx); err == nil && boot > 0 {
		at := time.Unix(int64(boot), 0).UTC()
		facts.BootedAt = &at
	}
	check := UpdateCheckState()
	facts.Checked = check.CheckedAt != nil && check.Error == ""
	if check.Latest != nil && release.Newer(check.Latest.Version, release.Version) {
		facts.Update = check.Latest.Version
	}

	return facts
}
