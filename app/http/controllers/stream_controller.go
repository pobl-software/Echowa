package controllers

import (
	"context"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/sse"
)

// StreamController answers the Server-Sent Events routes (routes/streams.go).
type StreamController struct{}

func NewStreamController() *StreamController {
	return &StreamController{}
}

// AppLogs follows the logs of one of the app's deploys, or of its pooler's
// (services.FollowAppLogs).
func (r *StreamController) AppLogs(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	req := ctx.Request()
	query, err := services.ParseLogQuery(req.Query("tail"), req.Query("since"), req.Query("source"), req.Query("deploy"), req.Header("Last-Event-ID"), time.Now())
	if err != nil {
		return ctx.Response().String(http.StatusUnprocessableEntity, err.Error())
	}

	return sse.Serve(ctx, func(c context.Context, send func(sse.Event) error) error {
		return services.FollowAppLogs(c, &app, query, send)
	})
}

// Deployment follows a deployment's log and status until it finishes
// (services.FollowDeployment).
func (r *StreamController) Deployment(ctx http.Context) http.Response {
	var deployment models.Deployment
	if resp := findOr404(ctx, "deployment", &deployment); resp != nil {
		return resp
	}
	offset := 0
	if id := ctx.Request().Header("Last-Event-ID"); id != "" {
		n, err := strconv.Atoi(id)
		if err != nil || n < 0 {
			return ctx.Response().String(http.StatusUnprocessableEntity, "Last-Event-ID must be a log offset")
		}
		offset = n
	}

	return sse.Serve(ctx, func(c context.Context, send func(sse.Event) error) error {
		return services.FollowDeployment(c, deployment.ID, offset, send)
	})
}
