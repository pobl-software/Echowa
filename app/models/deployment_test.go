package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeploymentStatusTransitions(t *testing.T) {
	allowed := map[DeploymentStatus][]DeploymentStatus{
		DeploymentQueued:    {DeploymentPulling, DeploymentFailed},
		DeploymentPulling:   {DeploymentDeploying, DeploymentFailed},
		DeploymentDeploying: {DeploymentSucceeded, DeploymentFailed, DeploymentRolledBack},
	}
	all := []DeploymentStatus{DeploymentQueued, DeploymentPulling, DeploymentDeploying,
		DeploymentSucceeded, DeploymentFailed, DeploymentRolledBack}

	for _, from := range all {
		for _, to := range all {
			want := false
			for _, next := range allowed[from] {
				want = want || next == to
			}
			assert.Equal(t, want, from.CanBecome(to), "%s -> %s", from, to)
		}
	}
}

func TestDeploymentStatusRunning(t *testing.T) {
	for _, s := range []DeploymentStatus{DeploymentQueued, DeploymentPulling, DeploymentDeploying} {
		assert.True(t, s.Running(), s)
	}
	for _, s := range []DeploymentStatus{DeploymentSucceeded, DeploymentFailed, DeploymentRolledBack} {
		assert.False(t, s.Running(), s)
	}
}
