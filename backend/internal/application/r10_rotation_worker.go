package application

import (
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"net"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
	"time"
)

func rotationMethod(kind string, secret vault.Secret) (ssh.AuthMethod, error) {
	if kind == "password" {
		return ssh.Password(secret.Password), nil
	}
	key, e := ssh.ParsePrivateKey([]byte(secret.PrivateKey))
	if e != nil {
		return nil, e
	}
	return ssh.PublicKeys(key), nil
}

func (a *App) rotationExecutionAsset(ctx context.Context, run vault.RotationRun, t vault.RotationTarget) (resource.Asset, error) {
	if e := a.db.RequireFeature(ctx, "automation.execute"); e != nil {
		return resource.Asset{}, e
	}
	return a.rotationAsset(ctx, run, t)
}
func (a *App) rotationAsset(ctx context.Context, run vault.RotationRun, t vault.RotationTarget) (resource.Asset, error) {
	subject, e := a.identity.Subject(ctx, run.ActorID)
	if e != nil {
		return resource.Asset{}, e
	}
	if !subject.Enabled {
		return resource.Asset{}, fault.Denied
	}
	if e = a.allowed(ctx, &subject.User, "credential.rotate"); e != nil {
		return resource.Asset{}, e
	}
	if run.ScheduleID != "" {
		if e = a.allowed(ctx, &subject.User, "task.manage"); e != nil {
			return resource.Asset{}, e
		}
		if e = automation.New(a.db).DefinitionPresent(ctx, run.ScheduleID); e != nil {
			return resource.Asset{}, e
		}
	}
	v, e := a.sessionAsset(ctx, &subject.User, t.AssetID)
	if e != nil {
		return v, e
	}
	if v.Protocol != "ssh" || v.Version != t.AssetVersion || v.CredentialID != t.CredentialID {
		return v, fault.Conflict
	}
	cred, e := a.vault.Get(ctx, t.CredentialID)
	if e != nil {
		return v, e
	}
	if cred.Version != t.CredentialVersion {
		return v, fault.Conflict
	}
	if e = a.enforcePolicy(ctx, &subject.User, t.AssetID, run.SourceIP, "connect", ""); e != nil {
		return v, e
	}
	return v, a.enforcePolicy(ctx, &subject.User, t.AssetID, run.SourceIP, "execute", "credential.rotate")
}

func (a *App) executeRotationJob(ctx context.Context, job jobs.Job) error {
	run, e := a.vault.RotationRun(ctx, job.ID)
	if jobs.IsEmpty(e) {
		if e = a.prepareScheduledRotation(ctx, job); e != nil {
			if errors.Is(e, fault.Conflict) {
				return nil
			}
			return e
		}
		run, e = a.vault.RotationRun(ctx, job.ID)
	}
	if e != nil {
		return e
	}
	for _, target := range run.Targets {
		if e = a.executeRotationTarget(ctx, job, run, target); e != nil {
			return e
		}
	}
	c, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return a.db.Within(c, func(c context.Context) error {
		targets, e := a.vault.RotationTargets(c, job.ID)
		if e != nil {
			return e
		}
		counts := map[vault.RotationResult]int{}
		for _, t := range targets {
			counts[t.State]++
			if e = automation.New(a.db).TargetDone(c, job.ID, automation.Target{AssetID: t.AssetID, State: string(t.State), Reason: t.Reason}); e != nil {
				return e
			}
		}
		state := "succeeded"
		switch {
		case counts[vault.RotationUnknown] > 0:
			state = "unknown"
		case counts[vault.RotationSucceeded] > 0 && counts[vault.RotationSucceeded] < len(targets):
			state = "partial"
		case counts[vault.RotationFailed] > 0:
			state = "failed"
		case counts[vault.RotationCancelled] > 0:
			state = "cancelled"
		}
		if e = jobs.New(a.db).Finish(c, job, state, ""); e != nil {
			return e
		}
		if e = automation.New(a.db).PauseUnknown(c, job.ID); e != nil {
			return e
		}
		return a.audit.Detail(c, run.ActorID, "rotation.completed", "rotation:"+job.ID, run.SourceIP, map[string]string{"id": job.ID, "state": state})
	})
}

func (a *App) executeRotationTarget(parent context.Context, job jobs.Job, run vault.RotationRun, t vault.RotationTarget) error {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	state := vault.RotationPending
	move := func(to vault.RotationResult, reason string) error {
		c, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		e := a.db.Within(c, func(c context.Context) error { return a.vault.MoveRotation(c, job, t.ID, state, to, reason) })
		if e == nil {
			state = to
		}
		return e
	}
	fail := func(reason string) error {
		if state == vault.RotationPending {
			return move(vault.RotationFailed, reason)
		}
		return move(vault.RotationUnknown, reason)
	}
	if ctx.Err() != nil {
		return move(vault.RotationCancelled, "cancelled_before_dispatch")
	}
	asset, e := a.rotationExecutionAsset(ctx, run, t)
	if e != nil {
		return fail("authorization_or_version_changed")
	}
	trust, e := a.connectivity.Get(ctx, asset.ID)
	if e != nil || trust.Host != asset.Host || trust.Port != asset.Port {
		return fail("host_key_untrusted")
	}
	var cred vault.Credential
	var previous ssh.AuthMethod
	var candidate vault.Secret
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(a.db).Fence(c, job); e != nil {
			return e
		}
		var e error
		cred, previous, e = a.vault.RotationAuth(c, t.CredentialID, run.ActorID, run.ID)
		if e != nil {
			return e
		}
		if cred.Version != t.CredentialVersion {
			return fault.Conflict
		}
		if trust.Fingerprint+":"+strings.ToLower(cred.Username) != t.AccountKey {
			return fault.Conflict
		}
		candidate, e = a.vault.RotationSecret(c, t.ID, run.ActorID, run.ID)
		return e
	})
	if e != nil {
		return fail("credential_unavailable")
	}
	next, e := rotationMethod(t.Kind, candidate)
	if e != nil {
		return fail("candidate_invalid")
	}
	authorize := func(c context.Context) error {
		_, e := a.rotationExecutionAsset(c, run, t)
		if e != nil {
			return e
		}
		latest, e := a.connectivity.Get(c, t.AssetID)
		if e != nil {
			return e
		}
		if latest.Version != trust.Version {
			return fault.Conflict
		}
		return nil
	}
	var transport connectivity.Dialer
	switch {
	case asset.AgentID != "":
		transport = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, asset.AgentID, run.ID, h, p, authorize)
		}
	case asset.JumpID != "":
		transport = a.jumpDialer(ctx, asset.JumpID, run.ActorID, run.ID)
	case asset.GatewayGroupID != "":
		transport = a.groupDialer(ctx, asset.GatewayGroupID, run.ActorID, run.ID, authorize)
	case asset.ProxyID != "":
		transport = a.proxyDialer(ctx, asset.ProxyID, run.ActorID, run.ID)
	}
	connect := func(method ssh.AuthMethod) (*ssh.Client, error) {
		c, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		return connectivity.ConnectUsing(c, trust, cred.Username, method, transport)
	}
	client, e := connect(previous)
	if e != nil {
		return fail("ssh_connect_failed")
	}
	defer client.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { client.Close() })
	defer closeOnCancel()
	var keyfile *vault.KeyFileRotation
	if t.Kind == "password" {
		session, e := client.NewSession()
		if e != nil {
			return fail("preflight_failed")
		}
		// Root and non-interactive sudo are supported; never prompt for a sudo password.
		cmd := "test $(uname -s) = Linux && command -v chpasswd >/dev/null && test $(id -u) = 0"
		if cred.Username != "root" {
			cmd = "test $(uname -s) = Linux && sudo -n -l chpasswd >/dev/null 2>&1"
		}
		e = session.Run(cmd)
		session.Close()
		if e != nil {
			return fail("linux_chpasswd_privilege_required")
		}
	} else {
		keyfile, e = vault.PrepareKeyFileRotation(client, cred.PublicKey, t.PublicKey)
		if e != nil {
			return fail("authorized_keys_preflight_failed")
		}
		defer keyfile.Close()
	}
	if e = authorize(ctx); e != nil {
		return fail("authorization_or_version_changed")
	}
	if e = move(vault.RotationApplying, ""); e != nil {
		return e
	}
	if t.Kind == "password" {
		e = vault.ApplyLinuxPassword(ctx, client, cred.Username, candidate.Password, cred.Username != "root")
	} else {
		e = keyfile.Stage()
	}
	if e != nil {
		return fail("remote_result_requires_review")
	}
	if e = move(vault.RotationVerifying, ""); e != nil {
		return e
	}
	verified, e := connect(next)
	if e != nil {
		return fail("candidate_verification_failed")
	}
	verified.Close()
	if keyfile != nil {
		if e = authorize(ctx); e != nil {
			return fail("authorization_or_version_changed")
		}
		if e = a.db.Within(ctx, func(c context.Context) error { return jobs.New(a.db).Fence(c, job) }); e != nil {
			return fail("cancel_before_key_commit")
		}
		if e = keyfile.Commit(); e != nil {
			return fail("key_commit_result_unknown")
		}
		verified, e = connect(next)
		if e != nil {
			return fail("candidate_verification_failed")
		}
		verified.Close()
	}
	old, e := connect(previous)
	if e == nil {
		old.Close()
		return fail("both_credentials_work")
	}
	// Transport failures are not evidence that the previous credential was revoked.
	if !strings.Contains(e.Error(), "unable to authenticate") || errors.Is(e, connectivity.ErrHostKey) {
		return fail("previous_verification_inconclusive")
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(a.db).Fence(c, job); e != nil {
			return e
		}
		latest, e := a.rotationExecutionAsset(c, run, t)
		if e != nil {
			return e
		}
		latestTrust, e := a.connectivity.Get(c, asset.ID)
		if e != nil {
			return e
		}
		if latestTrust.Version != trust.Version {
			return fault.Conflict
		}
		id, e := a.vault.ActivateRotation(c, t, cred.Username, candidate)
		if e != nil {
			return e
		}
		latest.CredentialID = id
		if _, e = a.resources.Save(c, latest); e != nil {
			return e
		}
		if e = a.vault.MoveRotation(c, job, t.ID, vault.RotationVerifying, vault.RotationSucceeded, ""); e != nil {
			return e
		}
		return a.audit.Detail(c, run.ActorID, "rotation.activated", "rotation:"+job.ID, run.SourceIP, map[string]string{"targetId": t.ID, "assetId": t.AssetID, "credentialId": id})
	})
	if e != nil {
		return fail("activation_requires_review")
	}
	return nil
}
