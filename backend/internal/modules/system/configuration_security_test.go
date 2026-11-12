package system

import (
	"slices"
	"testing"
)

func TestEverySecurityLivePropertyHasBarrierSemantics(t *testing.T) {
	for _, p := range PropertySchema() {
		if p.Effect == "security-live" && !slices.Contains(securityKeys, p.Key) {
			t.Fatalf("new security property requires restrictive merge: %s", p.Key)
		}
	}
}

func TestSecurityIntersectionNeverRelaxesDuringRollout(t *testing.T) {
	old := ConfigurationValues{}
	for _, p := range PropertySchema() {
		old[p.Key] = p.Default
	}
	old["login-force-totp-enabled"] = true
	old["password-expiration-period"] = 30
	next := ConfigurationValues{}
	for k, v := range old {
		next[k] = v
	}
	next["login-force-totp-enabled"] = false
	next["login-lock-enabled"] = false
	next["login-lock-failed-times"] = 10
	next["password-expiration-period"] = -1
	next["password-strength-type"] = "customize"
	next["password-strength-policy"] = `{"minLength":8,"minCharacterType":3,"mustNotContainUsername":false,"mustNotWeak":false,"mustNotBePalindrome":true}`
	got, e := restrictiveSecurity(old, next)
	if e != nil {
		t.Fatal(e)
	}
	if !got.Bool("login-force-totp-enabled") || !got.Bool("login-lock-enabled") || got.Int("login-lock-failed-times") != 5 || got.Int("password-expiration-period") != 30 {
		t.Fatal("relaxed safety boundary", got)
	}
	policy, e := effectivePasswordPolicy(got)
	if e != nil || policy.MinLength != 12 || policy.MinCharacterType != 3 || !policy.MustNotContainUsername || !policy.MustNotWeak || !policy.MustNotBePalindrome {
		t.Fatal("mixed password change lost restriction", policy, e)
	}
	// A second publish cannot discard an earlier pending stricter requirement.
	newer, e := restrictiveSecurity(got, next)
	if e != nil {
		t.Fatal(e)
	}
	p, e := effectivePasswordPolicy(newer)
	if e != nil || p != policy {
		t.Fatal(p, e)
	}
}
