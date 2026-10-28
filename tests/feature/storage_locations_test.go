package feature

import (
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/suite"

	"goravel/app/backups"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/tests"
)

const storageSecret = "s3-secret-0123456789abcdef"

type StorageLocationsTestSuite struct {
	suite.Suite
	tests.TestCase
	storage *backups.Fake
}

func TestStorageLocationsTestSuite(t *testing.T) {
	suite.Run(t, new(StorageLocationsTestSuite))
}

func (s *StorageLocationsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	s.storage = s.FakeStorage()
}

func storageForm(overrides map[string]any) map[string]any {
	form := map[string]any{
		"name":          "R2",
		"endpoint":      "https://abc123.r2.cloudflarestorage.com",
		"region":        "auto",
		"bucket":        "echowa-backups",
		"prefix":        "server-1",
		"access_key_id": "AKIAEXAMPLE",
		"secret_key":    storageSecret,
		"path_style":    false,
	}
	for k, v := range overrides {
		form[k] = v
	}

	return form
}

func (s *StorageLocationsTestSuite) createLocation() models.StorageLocation {
	loc := models.StorageLocation{
		Name: "R2", Endpoint: "https://abc123.r2.cloudflarestorage.com", Region: "auto", Bucket: "echowa-backups",
		Prefix: "server-1", AccessKeyID: "AKIAEXAMPLE", SecretKey: models.Secret(storageSecret),
	}
	s.Require().NoError(facades.Orm().Query().Create(&loc))

	return loc
}

func (s *StorageLocationsTestSuite) load(id uint) models.StorageLocation {
	var loc models.StorageLocation
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&loc))

	return loc
}

// check is the dialog's Test button: plain JSON, outside Inertia.
func (s *StorageLocationsTestSuite) check(b *browser, form map[string]any, status int) map[string]any {
	resp := b.request(nethttp.MethodPost, "/settings/storage/test", form, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})
	resp.AssertStatus(status)
	result, err := resp.Json()
	s.Require().NoError(err)

	return result
}

func (s *StorageLocationsTestSuite) TestSettingsListsLocationsWithoutTheSecret() {
	loc := s.createLocation()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)

	_, page := b.visit("/settings")

	page.AssertComponent("Settings/Index")
	rows := page.Props["storageLocations"].([]any)
	s.Require().Len(rows, 1)
	row := rows[0].(map[string]any)
	s.Equal(float64(loc.ID), row["id"])
	s.Equal("echowa-backups", row["bucket"])
	s.Equal("AKIAEXAMPLE", row["access_key_id"])
	s.NotContains(row, "secret_key")
	s.IsType("", page.Props["storagePrefix"])
}

func (s *StorageLocationsTestSuite) TestStoreNormalisesAndEncryptsTheSecret() {
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)
	b.visit("/settings")

	b.submit(nethttp.MethodPost, "/settings/storage", "/settings", storageForm(map[string]any{
		"name": " R2 ", "endpoint": "https://abc123.r2.cloudflarestorage.com/", "region": "AUTO", "prefix": "/server-1/",
	})).AssertHeader("Location", "/settings")

	var loc models.StorageLocation
	s.Require().NoError(facades.Orm().Query().Where("name", "R2").FirstOrFail(&loc))
	s.Equal("https://abc123.r2.cloudflarestorage.com", loc.Endpoint)
	s.Equal("auto", loc.Region)
	s.Equal("server-1", loc.Prefix)
	s.Equal(storageSecret, string(loc.SecretKey))
	var stored string
	s.Require().NoError(facades.DB().Table("storage_locations").Where("id", loc.ID).Value("secret_key", &stored))
	s.NotContains(stored, storageSecret)

	_, page := b.visit("/settings")
	s.Contains(page.Flash["success"], "Added R2")
}

func (s *StorageLocationsTestSuite) TestStoreValidates() {
	s.createLocation()
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	b.submit(nethttp.MethodPost, "/settings/storage", "/settings", map[string]any{
		"name": "R2", "endpoint": "http://s3.example.com", "region": "us east", "bucket": "My_Bucket",
		"prefix": "../up", "access_key_id": "", "secret_key": "",
	}).AssertHeader("Location", "/settings")

	_, page := b.visit("/settings")
	errs := page.Props["errors"].(map[string]any)
	for _, field := range []string{"name", "endpoint", "region", "bucket", "prefix", "access_key_id", "secret_key"} {
		s.Contains(errs, field)
	}
	s.Contains(errs["endpoint"], "Plain http:// works only for localhost")
}

func (s *StorageLocationsTestSuite) TestUpdateWithABlankSecretKeepsIt() {
	loc := s.createLocation()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret, "replacement-secret")
	b.visit("/settings")
	url := fmt.Sprintf("/settings/storage/%d", loc.ID)

	b.submit(nethttp.MethodPut, url, "/settings", storageForm(map[string]any{
		"name": "Backblaze", "secret_key": "", "path_style": true,
	})).AssertHeader("Location", "/settings")
	updated := s.load(loc.ID)
	s.Equal("Backblaze", updated.Name)
	s.True(updated.PathStyle)
	s.Equal(storageSecret, string(updated.SecretKey), "a blank secret keeps the stored one")

	b.submit(nethttp.MethodPut, url, "/settings", storageForm(map[string]any{"secret_key": "replacement-secret"})).
		AssertHeader("Location", "/settings")
	s.Equal("replacement-secret", string(s.load(loc.ID).SecretKey))
}

func (s *StorageLocationsTestSuite) TestDestroyLeavesTheBucketAlone() {
	loc := s.createLocation()
	s.Require().NoError(s.storage.Put(s.T().Context(), "server-1/shop-db/backup.dump", strings.NewReader("dump")))
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/settings/storage/%d", loc.ID), "/settings", nil).
		AssertHeader("Location", "/settings")

	count, err := facades.Orm().Query().Model(&models.StorageLocation{}).Count()
	s.Require().NoError(err)
	s.Zero(count)
	s.Equal([]string{"server-1/shop-db/backup.dump"}, s.storage.Keys())
}

func (s *StorageLocationsTestSuite) TestCheckRunsEveryStepOnTheFormsValues() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	result := s.check(b, storageForm(map[string]any{"bucket": "unsaved-bucket"}), nethttp.StatusOK)

	s.Equal(true, result["passed"])
	s.Len(result["steps"], 4)
	s.Require().Len(tests.OpenedStorage, 1)
	s.Equal("unsaved-bucket", tests.OpenedStorage[0].Bucket)
	s.Empty(s.storage.Keys(), "the check deletes its test object")
}

func (s *StorageLocationsTestSuite) TestCheckReportsTheFailingStep() {
	s.storage.Err = &smithy.GenericAPIError{Code: "InvalidAccessKeyId"}
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	result := s.check(b, storageForm(nil), nethttp.StatusOK)

	s.Equal(false, result["passed"])
	steps := result["steps"].([]any)
	s.Require().Len(steps, 1)
	s.Equal("The provider doesn't know this access key ID.", steps[0].(map[string]any)["error"])
}

func (s *StorageLocationsTestSuite) TestCheckValidatesBeforeConnecting() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	result := s.check(b, storageForm(map[string]any{"endpoint": "http://10.0.0.5:9000", "secret_key": ""}), nethttp.StatusUnprocessableEntity)

	errs := result["errors"].(map[string]any)
	s.Contains(errs, "endpoint")
	s.Contains(errs, "secret_key")
	s.NotContains(errs, "name", "Test doesn't need a name")
	s.Empty(tests.OpenedStorage)
}

func (s *StorageLocationsTestSuite) TestCheckOfASavedLocationUsesItsStoredSecret() {
	loc := s.createLocation()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)
	b.visit("/settings")

	s.check(b, storageForm(map[string]any{"id": loc.ID, "secret_key": ""}), nethttp.StatusOK)

	s.Require().Len(tests.OpenedStorage, 1)
	s.Equal(storageSecret, string(tests.OpenedStorage[0].SecretKey))
}
