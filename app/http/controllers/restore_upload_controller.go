package controllers

import (
	"errors"
	"mime"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// chunkWait is how long a chunk's request may take to arrive. A browser
// that stops part way through one would otherwise hold it open.
const chunkWait = 5 * time.Minute

// RestoreUploadController takes a dump from the browser in chunks, for a
// restore from a file (ADR 0034). The restore dialog previews it and
// restores it through RestoreController with ?upload=. Chunks come
// through routes/streams.go, past the request timeout.
type RestoreUploadController struct{}

func NewRestoreUploadController() *RestoreUploadController {
	return &RestoreUploadController{}
}

// Store starts an upload of the posted name and size, as JSON.
func (r *RestoreUploadController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	validator, err := ctx.Request().Validate(rules.RestoreUpload())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": "Choose a file to upload."})
	}
	upload, err := services.StartUpload(&app, ctx.Request().Input("name"), ctx.Request().InputInt64("size"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Json(http.StatusCreated, http.Json{"id": upload.ID, "chunk": services.UploadChunk})
}

// Show answers how much of the file has arrived, as JSON. It's in the web
// group, so the dialog asks now and then during a long upload to keep the
// session alive: the chunks, in routes/streams.go, never save it.
func (r *RestoreUploadController) Show(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	upload, ok := services.FindUpload(app.ID, ctx.Request().Route("upload"))
	if !ok {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "The upload isn't there any more. Choose the file again."})
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, http.Json{"received": upload.Received()})
}

// Update writes the request's body as the chunk that starts at ?offset=,
// and answers with how much of the file has arrived. A chunk that doesn't
// start there answers 409 with where it should.
func (r *RestoreUploadController) Update(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	upload, ok := services.FindUpload(app.ID, ctx.Request().Route("upload"))
	if !ok {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "The upload isn't there any more. Choose the file again."})
	}
	// Goravel reads a JSON or form body before the handler runs. The chunk
	// must reach it unread.
	if media, _, _ := mime.ParseMediaType(ctx.Request().Header("Content-Type")); media != "application/octet-stream" {
		return ctx.Response().Json(http.StatusUnsupportedMediaType, http.Json{"error": "Send the chunk as application/octet-stream."})
	}
	offset, err := strconv.ParseInt(ctx.Request().Query("offset"), 10, 64)
	if err != nil || offset < 0 {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "Say where the chunk starts with ?offset=."})
	}
	_ = nethttp.NewResponseController(ctx.Response().Writer()).SetReadDeadline(time.Now().Add(chunkWait))

	received, err := upload.Write(offset, ctx.Request().Origin().Body)
	var moved *services.UploadOffsetError
	switch {
	case errors.As(err, &moved):
		return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error(), "received": moved.Received})
	case errors.Is(err, services.ErrUploadGone):
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "The upload isn't there any more. Choose the file again."})
	case errors.Is(err, services.ErrChunkTooBig):
		return ctx.Response().Json(http.StatusRequestEntityTooLarge, http.Json{"error": err.Error()})
	case errors.Is(err, services.ErrUploadTooLong), errors.Is(err, services.ErrChunkCut):
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	case err != nil:
		facades.Log().Error(err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "Couldn't write the upload: " + err.Error()})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"received": received})
}

// Destroy cancels an upload no restore has taken.
func (r *RestoreUploadController) Destroy(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	if upload, ok := services.FindUpload(app.ID, ctx.Request().Route("upload")); ok {
		upload.Cancel()
	}

	return ctx.Response().NoContent()
}
