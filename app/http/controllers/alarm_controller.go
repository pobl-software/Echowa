package controllers

import (
	"context"
	"strings"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// AlarmController is the Alarms page: every time an alert went off,
// newest first, with what's firing now above.
type AlarmController struct{}

func NewAlarmController() *AlarmController {
	return &AlarmController{}
}

// Index lists the alarms, all or ?filter=unread. The list scrolls: each
// load asks for the ones before the oldest it has (?before=).
func (r *AlarmController) Index(ctx http.Context) http.Response {
	unread := ctx.Request().Query("filter") == "unread"
	before := uint(max(0, ctx.Request().QueryInt("before")))
	page, err := services.Alarms(ctx.Context(), unread, before)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	meta := gonertia.ScrollMetadata{PageName: "before", NextPage: page.Before}
	if before > 0 {
		meta.CurrentPage = before
	}
	filter := "all"
	if unread {
		filter = "unread"
	}

	return inertia.Render(ctx, "Alarms", gonertia.Props{
		"filter": filter,
		"alarms": gonertia.Scroll(page.Alarms, gonertia.WithMetadata(meta)),
		"firing": func(c context.Context) (any, error) {
			return services.FiringNow(c)
		},
	})
}

// Read marks an alarm as read and opens the page it's about.
func (r *AlarmController) Read(ctx http.Context) http.Response {
	var alarm models.Alarm
	if resp := findOr404(ctx, "alarm", &alarm); resp != nil {
		return resp
	}
	if err := services.MarkAlarmRead(ctx.Context(), alarm.ID); err != nil {
		inertia.Flash(ctx, "error", "Couldn't mark the alarm as read.")
		return inertia.Back(ctx)
	}
	// Paths come from Echowa's own events, but never leave the site.
	if strings.HasPrefix(alarm.Path, "/") && !strings.HasPrefix(alarm.Path, "//") {
		return inertia.Redirect(ctx, alarm.Path)
	}

	return inertia.Back(ctx)
}

// ReadAll marks every alarm up to the newest the page showed as read.
func (r *AlarmController) ReadAll(ctx http.Context) http.Response {
	if err := services.MarkAlarmsRead(ctx.Context(), uint(max(0, ctx.Request().InputInt("through")))); err != nil {
		inertia.Flash(ctx, "error", "Couldn't mark the alarms as read.")
	}

	return inertia.Back(ctx)
}
