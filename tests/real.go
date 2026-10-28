//go:build docker

package tests

import (
	"testing"

	"goravel/app/backups"
	"goravel/app/docker"
	"goravel/app/services"
)

// UseReal points Echowa at engine and at real S3 buckets until the test
// ends, for the docker integration tests (deploy/dev-vm.sh test-docker).
func (tc *TestCase) UseReal(t *testing.T, engine docker.Client) {
	t.Helper()
	client, open := services.Docker, backups.Open
	services.Docker = func() docker.Client { return engine }
	backups.Open = realStorage
	t.Cleanup(func() { services.Docker, backups.Open = client, open })
}
