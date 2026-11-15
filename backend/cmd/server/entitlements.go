package main

import (
	"puppeteer/backend/internal/modules/entitlements"
)

func loadEntitlements() (entitlements.CapabilityProvider, *entitlements.Verifier, error) {
	return entitlements.LoadEnvironment()
}
