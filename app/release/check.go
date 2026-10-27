package release

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"runtime"
	"time"
)

// Check prints the running version and the channel's newest release, once
// its signature checks out: `echowa update --check`.
func Check(ctx context.Context, w io.Writer) error {
	fmt.Fprintf(w, "running  %s\n", Version)
	key, err := PublicKey()
	if err != nil {
		return err
	}
	channel, err := Channel()
	if err != nil {
		return err
	}

	m, err := Latest(ctx, &nethttp.Client{Timeout: 30 * time.Second}, channel, key)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "newest   %s, published %s, signature ok (%s)\n", m.Version, m.PublishedAt.Format(time.DateOnly), channel)
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if b, ok := m.Binaries[platform]; ok {
		fmt.Fprintf(w, "         %s for %s\n", b.Path, platform)
	} else {
		fmt.Fprintf(w, "         no build for %s\n", platform)
	}

	switch {
	case !IsRelease(Version):
		fmt.Fprintln(w, "This is a dev build, which doesn't update itself.")
	case Newer(m.Version, Version):
		fmt.Fprintf(w, "%s is available.\n", m.Version)
	default:
		fmt.Fprintln(w, "Up to date.")
	}

	return nil
}
