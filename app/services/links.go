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
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Template string `json:"template"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	// Pooler is where apps reach it through its connection pooler, while
	// pooling is on (ADR 0035).
	Pooler *Endpoint  `json:"pooler"`
	Status LiveStatus `json:"status"`
}

// Endpoint is a host and port on Echowa's network.
type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
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
		row := LinkedDatabase{
			ID: app.ID, Name: app.Name, Template: *app.Template,
			Host: app.ServiceName(), Port: app.Port, Status: AppLiveStatus(ctx, &app),
		}
		if tmpl, ok := templates.Find(*app.Template); ok && tmpl.Pooler != nil && app.Pooling {
			row.Pooler = &Endpoint{Host: app.PoolerServiceName(), Port: tmpl.Pooler.Port}
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// LinkedService is a row of an app's Services tab: a custom app it's
// linked to, such as a queue worker or a PDF renderer (ADR 0037).
type LinkedService struct {
	ID     uint       `json:"id"`
	Name   string     `json:"name"`
	Image  string     `json:"image"`
	Host   string     `json:"host"`
	Port   int        `json:"port"`
	Domain *string    `json:"domain"`
	Status LiveStatus `json:"status"`
}

// ServiceChoice is a custom app the Services tab offers to link.
type ServiceChoice struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Repository string `json:"repository"`
}

// AppRef names an app.
type AppRef struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// LinkedServices lists the custom apps appID is linked to, with their live
// status.
func LinkedServices(ctx context.Context, appID uint) ([]LinkedService, error) {
	var apps []models.App
	err := facades.Orm().WithContext(ctx).Query().Where("id IN "+linkedIDs, appID).Where("template IS NULL").Order("name").Get(&apps)
	if err != nil {
		return nil, err
	}

	rows := make([]LinkedService, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, LinkedService{
			ID: app.ID, Name: app.Name, Image: app.Repository + ":" + app.DefaultTag,
			Host: app.ServiceName(), Port: app.Port, Domain: app.Domain, Status: AppLiveStatus(ctx, &app),
		})
	}

	return rows, nil
}

// hasServices is the IDs of the apps that have services of their own.
const hasServices = "(SELECT app_links.app_id FROM app_links JOIN apps linked ON linked.id = app_links.linked_app_id WHERE linked.template IS NULL)"

// UnlinkedServices lists the custom apps appID can link as services: not
// itself, not linked yet, and with no services of their own, so services
// stay one level deep (ADR 0037).
func UnlinkedServices(ctx context.Context, appID uint) ([]ServiceChoice, error) {
	choices := []ServiceChoice{}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Select("id", "name", "repository").
		Where("template IS NULL").Where("id <> ?", appID).Where("id NOT IN "+linkedIDs, appID).
		Where("id NOT IN " + hasServices).Order("name").Get(&choices)

	return choices, err
}

// HasServices reports whether appID has services of its own, which keeps
// it from being another app's service.
func HasServices(ctx context.Context, appID uint) (bool, error) {
	return facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Where("id", appID).Where("id IN " + hasServices).Exists()
}

// ServiceOf lists the custom apps that have appID as a service. An app
// that's a service has none of its own (ADR 0037).
func ServiceOf(ctx context.Context, appID uint) ([]AppRef, error) {
	refs := []AppRef{}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Select("id", "name").
		Where("template IS NULL").Where("id IN (SELECT app_id FROM app_links WHERE linked_app_id = ?)", appID).
		Order("name").Get(&refs)

	return refs, err
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
