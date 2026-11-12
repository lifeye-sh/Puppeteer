package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"next-terminal/backend/internal/application"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/platform/deployment"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func restoreDeployment(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: restore run|status|activate|rollback|resume")
	}
	f := flag.NewFlagSet("restore "+args[0], flag.ContinueOnError)
	archive := f.String("archive", "", "trusted encrypted backup path")
	identity := f.String("identity-file", "", "offline age recovery key file")
	trusted := f.Bool("trust-source", false, "trust source-defined SQL in archive")
	parent := f.String("workspace", os.Getenv("NT_RESTORE_DIRECTORY"), "independent restore directory")
	id := f.String("restore-id", "", "restore identifier")
	endpoint := f.String("health-url", "http://server:8089/readyz", "new runtime readiness URL")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 || *parent == "" {
		return errors.New("restore workspace required")
	}
	profilePath, storagePath := os.Getenv("NT_RUNTIME_PROFILE"), os.Getenv("NT_STORAGE_PROFILE")
	if args[0] != "status" {
		if e := deployment.LoadSecrets(); e != nil {
			return e
		}
		release, e := backup.AcquireRestoreLock(*parent)
		if e != nil {
			return e
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if args[0] == "run" {
		if !*trusted || *archive == "" || *identity == "" {
			return errors.New("archive, identity-file and --trust-source required")
		}
		source, e := runtimeprofile.FromEnvironment()
		if e != nil {
			return e
		}
		admin := os.Getenv("NT_RESTORE_ADMIN_URL")
		if admin == "" {
			admin = os.Getenv("NT_DATABASE_URL")
		}
		plan, e := application.PrepareDeploymentRestore(ctx, *archive, *identity, *parent, profilePath, storagePath, admin, source)
		if e != nil {
			if plan.ID != "" {
				json.NewEncoder(os.Stdout).Encode(map[string]string{"restoreId": plan.ID, "state": "failed"})
			}
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"restoreId": plan.ID, "state": "prepared", "next": "start both services, verify readiness, then activate"})
	}
	if filepath.Base(*id) != *id || !strings.HasPrefix(*id, "restore-") || len(*id) > 80 {
		return errors.New("valid restore-id required")
	}
	events, e := backup.ReadRestoreEvents(*parent, *id)
	if e != nil {
		return e
	}
	if args[0] == "status" {
		return json.NewEncoder(os.Stdout).Encode(events)
	}
	file, e := os.Open(filepath.Join(*parent, *id, "plan.json"))
	if e != nil {
		return e
	}
	var plan application.RestorePlan
	e = json.NewDecoder(io.LimitReader(file, 2<<20)).Decode(&plan)
	file.Close()
	if e != nil || plan.ID != *id {
		return errors.New("restore plan unavailable")
	}
	if e = plan.Next.Validate(); e != nil {
		return e
	}
	if e = plan.Old.Validate(); e != nil {
		return e
	}
	current, e := runtimeprofile.FromEnvironment()
	if e != nil {
		return e
	}
	log, e := backup.OpenRestoreLog(*parent, *id)
	if e != nil {
		return e
	}
	defer log.Close()
	opened := false
	for _, event := range events {
		if event.State == "writes_opened" || event.State == "completed" {
			opened = true
		}
	}
	switch args[0] {
	case "activate":
		if !plan.Verified {
			return errors.New("restore verification incomplete")
		}
		if current.RestoreID != *id || current.DatabaseURL != plan.Next.DatabaseURL {
			return errors.New("active runtime does not match restore")
		}
		if current.WritesEnabled && opened {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"restoreId": *id, "state": "completed"})
		}
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, e := client.Get(*endpoint)
		if e != nil {
			return errors.New("new runtime readiness failed")
		}
		defer response.Body.Close()
		var ready struct {
			RestoreID string `json:"restoreId"`
			Frozen    bool   `json:"frozen"`
			Status    string `json:"status"`
		}
		e = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&ready)
		if e != nil || response.StatusCode != 200 || ready.Status != "ready" || ready.RestoreID != *id || !ready.Frozen {
			return errors.New("new frozen runtime not confirmed")
		}
		if e = log.Append("writes_opened"); e != nil {
			return e
		}
		next := plan.Next
		next.WritesEnabled = true
		if e = runtimeprofile.Write(profilePath, next); e != nil {
			return e
		}
		if e = log.Append("completed"); e != nil {
			return e
		}
	case "rollback":
		if opened || current.WritesEnabled && current.RestoreID == *id {
			return errors.New("writes were opened; automatic rollback is prohibited")
		}
		if current.DatabaseURL != plan.Next.DatabaseURL && current.DatabaseURL != plan.Old.DatabaseURL {
			return errors.New("active runtime does not match restore")
		}
		nextFence, e := application.FenceOffline(ctx, plan.Next.DatabaseURL)
		if e != nil {
			return e
		}
		defer nextFence.Close(ctx)
		oldFence, e := application.FenceOffline(ctx, plan.Old.DatabaseURL)
		if e != nil {
			return e
		}
		defer oldFence.Close(ctx)
		if e = runtimeprofile.AtomicJSON(storagePath, runtimeprofile.Storage{DatabaseURL: plan.Old.DatabaseURL, RestoreID: plan.Old.RestoreID}); e != nil {
			return e
		}
		if e = runtimeprofile.Write(profilePath, plan.Old); e != nil {
			return e
		}
		if e = log.Append("rolled_back"); e != nil {
			return e
		}
	case "resume":
		if opened {
			return errors.New("writes already opened; use status or activate")
		}
		if current.DatabaseURL != plan.Old.DatabaseURL && current.DatabaseURL != plan.Next.DatabaseURL {
			return errors.New("runtime changed since restore preparation")
		}
		oldFence, e := application.FenceOffline(ctx, plan.Old.DatabaseURL)
		if e != nil {
			return e
		}
		defer oldFence.Close(ctx)
		adminDSN := os.Getenv("NT_RESTORE_ADMIN_URL")
		if adminDSN == "" {
			adminDSN = os.Getenv("NT_DATABASE_URL")
		}
		if e = application.ResumeRestoreTarget(ctx, adminDSN, *parent, &plan, log); e != nil {
			return e
		}
		if e = runtimeprofile.AtomicJSON(storagePath, runtimeprofile.Storage{DatabaseURL: plan.Next.DatabaseURL, RestoreID: *id}); e != nil {
			return e
		}
		if e = runtimeprofile.Write(profilePath, plan.Next); e != nil {
			return e
		}
		if e = log.Append("switching"); e != nil {
			return e
		}
	default:
		return errors.New("unknown restore command")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"restoreId": *id, "state": args[0]})
}
func readRestoreEvents(path string) ([]backup.RestoreEvent, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 262144))
	events := []backup.RestoreEvent{}
	for {
		var v backup.RestoreEvent
		e = decoder.Decode(&v)
		if e == io.EOF {
			return events, nil
		}
		if e != nil {
			return nil, errors.New("invalid restore event log")
		}
		events = append(events, v)
	}
}
