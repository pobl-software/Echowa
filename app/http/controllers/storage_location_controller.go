package controllers

import (
	"fmt"
	"github.com/goravel/framework/contracts/http"

	"goravel/app/backups"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// StorageLocationController manages the S3 storage locations on the
// Settings page (ADR 0034). Secret keys go in, never out.
type StorageLocationController struct{}

func NewStorageLocationController() *StorageLocationController {
	return &StorageLocationController{}
}

func (r *StorageLocationController) Store(ctx http.Context) http.Response {
	in, resp := r.validate(ctx, 0)
	if resp != nil {
		return resp
	}

	loc, err := services.CreateStorageLocation(ctx, in)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Added "+loc.Name+".")

	return inertia.Redirect(ctx, "/settings")
}

func (r *StorageLocationController) Update(ctx http.Context) http.Response {
	var loc models.StorageLocation
	if resp := findOr404(ctx, "location", &loc); resp != nil {
		return resp
	}
	in, resp := r.validate(ctx, loc.ID)
	if resp != nil {
		return resp
	}
	if in.MovesBackups(&loc) {
		count, err := services.StorageLocationBackups(ctx, loc.ID)
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		if count > 0 {
			moved := "This location has backups, so its endpoint and bucket can't change. Add a new location instead."
			return failed(ctx, map[string]string{"endpoint": moved, "bucket": moved})
		}
	}

	if err := services.UpdateStorageLocation(ctx, &loc, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Saved "+loc.Name+".")

	return inertia.Redirect(ctx, "/settings")
}

func (r *StorageLocationController) Destroy(ctx http.Context) http.Response {
	var loc models.StorageLocation
	if resp := findOr404(ctx, "location", &loc); resp != nil {
		return resp
	}

	count, err := services.StorageLocationBackups(ctx, loc.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if err := services.DeleteStorageLocation(ctx, &loc); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if count > 0 {
		inertia.Flash(ctx, "success", fmt.Sprintf("Deleted %s and Echowa's records of its %s. They're still in the bucket.", loc.Name, backupCount(count)))
	} else {
		inertia.Flash(ctx, "success", "Deleted "+loc.Name+".")
	}

	return inertia.Redirect(ctx, "/settings")
}

// Check tests the form's values, saved or not, and answers with each
// step as plain JSON. With an id and a blank secret key it uses the
// stored key, so editing a location can test it without retyping it.
func (r *StorageLocationController) Check(ctx http.Context) http.Response {
	var loc models.StorageLocation
	if id := ctx.Request().InputInt("id"); id > 0 {
		if err := facades.Orm().WithContext(ctx).Query().Where("id", id).FirstOrFail(&loc); err != nil {
			return ctx.Response().Json(http.StatusNotFound, http.Json{"message": "No such storage location."})
		}
	}

	validator, err := ctx.Request().Validate(rules.StorageLocationCheck(loc.ID), rules.StorageLocationFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"errors": inertia.ValidationMessages(validator.Errors())})
	}
	var in services.StorageLocationInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	in.Apply(&loc)

	steps := services.CheckStorageLocation(ctx, &loc)

	return ctx.Response().Json(http.StatusOK, http.Json{"steps": steps, "passed": backups.Passed(steps)})
}

func (r *StorageLocationController) validate(ctx http.Context, id uint) (services.StorageLocationInput, http.Response) {
	var in services.StorageLocationInput
	validator, err := ctx.Request().Validate(rules.StorageLocation(id), rules.StorageLocationFilters())
	if err != nil {
		return in, inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return in, inertia.Back(ctx)
	}
	if err := validator.Bind(&in); err != nil {
		return in, inertia.ServerError(ctx, err)
	}

	return in, nil
}

func backupCount(n int64) string {
	if n == 1 {
		return "1 backup"
	}

	return fmt.Sprintf("%d backups", n)
}
