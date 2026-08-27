package service

import (
	"os"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const providerAPIKeyRefCredentialKey = "api_key_ref"

var providerAPIKeyEnvName = regexp.MustCompile(`^SUB2API_PROVIDER_[A-Z0-9_]*API_KEY$`)

var errInvalidProviderAPIKeyReference = infraerrors.BadRequest(
	"INVALID_PROVIDER_API_KEY_REFERENCE",
	"provider API key reference is invalid or unavailable",
)

func resolveProviderAPIKeyReference(ref string) (string, bool) {
	const prefix = "env:"
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(ref, prefix)
	if len(name) > 160 || !providerAPIKeyEnvName.MatchString(name) {
		return "", false
	}
	value, ok := os.LookupEnv(name)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func (a *Account) hasProviderAPIKeyReference() bool {
	if a == nil || a.Credentials == nil {
		return false
	}
	_, ok := a.Credentials[providerAPIKeyRefCredentialKey]
	return ok
}

func (a *Account) resolveProviderAPIKeyReference() string {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey || a.Credentials == nil {
		return ""
	}
	ref := credentialValueString(a.Credentials[providerAPIKeyRefCredentialKey])
	value, ok := resolveProviderAPIKeyReference(ref)
	if !ok {
		return ""
	}
	return value
}

func validateProviderAPIKeyReference(platform, accountType string, credentials map[string]any) error {
	if credentials == nil {
		return nil
	}
	rawRef, hasRef := credentials[providerAPIKeyRefCredentialKey]
	if !hasRef {
		return nil
	}
	if platform != PlatformOpenAI || accountType != AccountTypeAPIKey {
		return errInvalidProviderAPIKeyReference
	}
	if strings.TrimSpace(credentialValueString(credentials["api_key"])) != "" {
		return errInvalidProviderAPIKeyReference
	}
	ref, ok := rawRef.(string)
	if !ok {
		return errInvalidProviderAPIKeyReference
	}
	if _, ok := resolveProviderAPIKeyReference(ref); !ok {
		return errInvalidProviderAPIKeyReference
	}
	credentials[providerAPIKeyRefCredentialKey] = strings.TrimSpace(ref)
	return nil
}
