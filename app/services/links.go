package services

import (
	"context"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// LinkedDatabase is a row of an app's Databases tab: a template app it's
// linked to (ADR 0028), and where other apps reach it.
type LinkedDatabase struct {
	ID       uint       `json:"id"`
	Name     string     `json:"name"`
	Template string     `json:"template"`
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	Status   LiveStatus `json:"status"`
}

// DatabaseChoice is a template app the Databases tab offers to link.
type DatabaseChoice struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Template string `json:"template"`
}

const linkedIDs = "(SELECT linked_app_id FROM app_links WHERE app_id = ?)"

// LinkedDatabases lists the template apps appID is linked to, with their
// live status.
func LinkedDatabases(ctx context.Context, appID uint) ([]LinkedDatabase, error) {
	var apps []models.App
	err := facades.Orm().WithContext(ctx).Query().Where("id IN "+linkedIDs, appID).Where("template IS NOT NULL").Order("name").Get(&apps)
	if err != nil {
		return nil, err
	}

	rows := make([]LinkedDatabase, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, LinkedDatabase{
			ID: app.ID, Name: app.Name, Template: *app.Template,
			Host: app.ServiceName(), Port: app.Port, Status: AppLiveStatus(ctx, &app),
		})
	}

	return rows, nil
}

// UnlinkedDatabases lists the template apps appID isn't linked to.
func UnlinkedDatabases(ctx context.Context, appID uint) ([]DatabaseChoice, error) {
	choices := []DatabaseChoice{}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Select("id", "name", "template").
		Where("template IS NOT NULL").Where("id NOT IN "+linkedIDs, appID).Order("name").Get(&choices)

	return choices, err
}

// LinkApps links appID to each of ids. Linking twice changes nothing.
func LinkApps(ctx context.Context, appID uint, ids ...uint) error {
	for _, id := range ids {
		link := models.AppLink{AppID: appID, LinkedAppID: id}
		if err := facades.Orm().WithContext(ctx).Query().FirstOrCreate(&link, link); err != nil {
			return err
		}
	}

	return nil
}

// UnlinkApp removes the link from appID to linkedID. Both apps stay.
func UnlinkApp(ctx context.Context, appID, linkedID uint) error {
	_, err := facades.Orm().WithContext(ctx).Query().Where("app_id", appID).Where("linked_app_id", linkedID).Delete(&models.AppLink{})

	return err
}

// allLinks maps each app to the apps it's linked to, by ID.
func allLinks(ctx context.Context) (map[uint][]uint, error) {
	var links []models.AppLink
	if err := facades.Orm().WithContext(ctx).Query().Order("id").Get(&links); err != nil {
		return nil, err
	}

	ids := map[uint][]uint{}
	for _, link := range links {
		ids[link.AppID] = append(ids[link.AppID], link.LinkedAppID)
	}

	return ids, nil
}

// TemplateInput is the New app form as it's filled in for tmpl: the
// template's image, port and healthcheck, one replica and no domain.
func TemplateInput(tmpl templates.Template, name string) AppInput {
	return AppInput{
		Name: name, Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag,
		Port: tmpl.Port, Replicas: 1, HealthcheckCmd: &tmpl.Healthcheck,
	}
}
