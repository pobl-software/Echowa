package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"goravel/app/docker"
)

func TestServiceStatusReadsZeroDesiredAsStoppedAndStopping(t *testing.T) {
	original := Docker
	t.Cleanup(func() { Docker = original })
	cases := []struct {
		desired, running uint64
		want             string
	}{
		{0, 1, StateStopping},
		{0, 0, StateStopped},
		{1, 1, StateRunning},
		{2, 1, StateDegraded},
		{1, 0, StateDown},
	}
	for _, c := range cases {
		fake := &docker.Fake{Status: func(name string) (docker.ServiceStatus, error) {
			return docker.ServiceStatus{Name: name, Desired: c.desired, Running: c.running}, nil
		}}
		Docker = func() docker.Client { return fake }

		got := serviceStatus(context.Background(), "echowa-blog")

		assert.Equal(t, c.want, got.State, "desired %d, running %d", c.desired, c.running)
	}
}
