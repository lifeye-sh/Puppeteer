package application

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"puppeteer/backend/internal/modules/authorization"
	"puppeteer/backend/internal/modules/identity"
)

func (a *App) grantBatch(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.require(r, "identity.manage"); err != nil {
		return err
	}
	var in authorization.GrantBatch
	if err := decode(w, r, &in); err != nil {
		return err
	}
	var result authorization.GrantBatchResult
	err := a.mutate(r, "grant.batch.saved", func(ctx context.Context, u *identity.User) (string, error) {
		var err error
		result, err = a.authz.SaveGrantBatch(ctx, in)
		if err != nil {
			return "", err
		}
		for _, item := range result.Items {
			if item.Status != "created" {
				continue
			}
			if err = a.audit.Detail(ctx, u.ID, "grant.saved", requestID(r), sourceIP(r), map[string]string{"target": item.ID, "userId": item.UserID, "departmentId": item.DepartmentID, "resourceId": item.ResourceID, "source": "batch"}); err != nil {
				return "", err
			}
		}
		return "created=" + strconv.Itoa(result.Created) + ",skipped=" + strconv.Itoa(result.Skipped), nil
	})
	if err != nil {
		var invalid *authorization.GrantBatchInvalid
		if errors.As(err, &invalid) {
			return problem(422, "grant_batch_invalid", invalid.Message+"；本批次未写入")
		}
		return err
	}
	writeJSON(w, 200, result)
	return nil
}
