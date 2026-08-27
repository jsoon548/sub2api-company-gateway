package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const providerCredentialRefTestEnv = "SUB2API_PROVIDER_INTERNAL_INFERENCE_TEST_API_KEY"

func TestProviderAPIKeyReferenceResolvesWithoutPersistingSecret(t *testing.T) {
	t.Setenv(providerCredentialRefTestEnv, "synthetic-provider-secret")
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv,
		},
	}

	require.Equal(t, "synthetic-provider-secret", account.GetCredential("api_key"))
	require.True(t, account.IsSchedulable())
	require.NotContains(t, account.Credentials, "api_key")
}

func TestProviderAPIKeyReferenceFailsClosedWhenEnvironmentIsMissing(t *testing.T) {
	t.Setenv(providerCredentialRefTestEnv, "")
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv,
		},
	}

	require.Empty(t, account.GetCredential("api_key"))
	require.False(t, account.IsSchedulable())
}

func TestProviderAPIKeyReferenceSurvivesCredentialUpdateWhenOmitted(t *testing.T) {
	existing := map[string]any{
		providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv,
		"base_url":                     "https://old.example.com/v1",
	}
	incoming := map[string]any{
		"model_mapping": map[string]any{"advanced-model": "qwen3.8-max"},
	}

	merged := MergePreservingSensitiveCreds(existing, incoming)

	require.Equal(t, "env:"+providerCredentialRefTestEnv, merged[providerAPIKeyRefCredentialKey])
	require.NotContains(t, merged, "base_url", "ordinary credential fields retain full-object PUT semantics")
	require.False(t, IsSensitiveCredentialKey(providerAPIKeyRefCredentialKey), "the safe reference name remains visible in admin DTOs")
}

func TestValidateProviderAPIKeyReference(t *testing.T) {
	t.Setenv(providerCredentialRefTestEnv, "synthetic-provider-secret")
	valid := map[string]any{providerAPIKeyRefCredentialKey: " env:" + providerCredentialRefTestEnv + " "}
	require.NoError(t, validateProviderAPIKeyReference(PlatformOpenAI, AccountTypeAPIKey, valid))
	require.Equal(t, "env:"+providerCredentialRefTestEnv, valid[providerAPIKeyRefCredentialKey])

	tests := []struct {
		name        string
		platform    string
		accountType string
		credentials map[string]any
	}{
		{name: "literal and reference", platform: PlatformOpenAI, accountType: AccountTypeAPIKey, credentials: map[string]any{"api_key": "literal", providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv}},
		{name: "wrong platform", platform: PlatformAnthropic, accountType: AccountTypeAPIKey, credentials: map[string]any{providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv}},
		{name: "wrong account type", platform: PlatformOpenAI, accountType: AccountTypeOAuth, credentials: map[string]any{providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv}},
		{name: "arbitrary environment", platform: PlatformOpenAI, accountType: AccountTypeAPIKey, credentials: map[string]any{providerAPIKeyRefCredentialKey: "env:PATH"}},
		{name: "non string", platform: PlatformOpenAI, accountType: AccountTypeAPIKey, credentials: map[string]any{providerAPIKeyRefCredentialKey: true}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			require.ErrorIs(t, validateProviderAPIKeyReference(testCase.platform, testCase.accountType, testCase.credentials), errInvalidProviderAPIKeyReference)
		})
	}

	t.Setenv(providerCredentialRefTestEnv, "")
	require.ErrorIs(t, validateProviderAPIKeyReference(PlatformOpenAI, AccountTypeAPIKey, map[string]any{
		providerAPIKeyRefCredentialKey: "env:" + providerCredentialRefTestEnv,
	}), errInvalidProviderAPIKeyReference)
}
