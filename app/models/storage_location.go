package models

import (
	"github.com/goravel/framework/database/orm"
)

// StorageLocation is an S3 bucket that database backups go to (ADR 0034).
// Any S3-compatible provider works.
type StorageLocation struct {
	orm.Model
	Name string `json:"name"`
	// Endpoint is a URL without a path, such as
	// https://<account>.r2.cloudflarestorage.com. It's https unless its
	// host is loopback.
	Endpoint string `json:"endpoint"`
	// Region is what requests are signed for: "auto" on R2.
	Region string `json:"region"`
	Bucket string `json:"bucket"`
	// Prefix goes before every key, without a leading or trailing slash.
	// Empty puts backups at the bucket's root.
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"access_key_id"`
	// Never serialised: props end up in the page HTML and JSON.
	SecretKey Secret `json:"-"`
	// PathStyle puts the bucket in the path rather than the host name,
	// which some providers and self-hosted servers need.
	PathStyle bool `json:"path_style"`
}
