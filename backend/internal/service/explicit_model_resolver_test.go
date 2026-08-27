//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type explicitModelCatalogStub struct {
	find  func(ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error)
	calls int
}

func (s *explicitModelCatalogStub) FindApprovedExplicitModel(_ context.Context, input ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error) {
	s.calls++
	if s.find == nil {
		return nil, nil
	}
	return s.find(input)
}

func explicitModelInput(profile, model string) ExplicitModelResolveInput {
	return ExplicitModelResolveInput{
		AuthenticatedUser:      ExplicitModelAuthenticatedUser{ID: 42, Status: StatusActive, Role: RoleUser},
		Access:                 ExplicitModelGroupAccessContext{GroupID: 7},
		RequestedLogicalModel:  model,
		ProtocolProfileVersion: profile,
	}
}

func TestExplicitModelResolverPreservesLogicalAndProviderFactsAcrossProfiles(t *testing.T) {
	catalog := &explicitModelCatalogStub{find: func(input ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error) {
		return &ExplicitModelApprovalSnapshot{
			EntryID: "approved-company-coder", GroupID: 7, ChannelID: 9,
			LogicalModel: input.RequestedLogicalModel, Platform: PlatformOpenAI,
			ResolvedProviderModel: "gpt-5.6-codex", SchedulableAccountScope: []int64{22, 11, 22},
			ConfigurationVersion: 3,
		}, nil
	}}
	resolver := NewExplicitModelResolver(catalog)

	for _, profile := range []string{ProtocolProfileAnthropicMessagesV1, ProtocolProfileOpenAIResponsesV1} {
		t.Run(profile, func(t *testing.T) {
			got, err := resolver.Resolve(context.Background(), explicitModelInput(profile, "company-coder"))
			require.NoError(t, err)
			require.Equal(t, "company-coder", got.RequestedLogicalModel)
			require.Equal(t, "company-coder", got.ApprovedModel.LogicalModel)
			require.Equal(t, "gpt-5.6-codex", got.ResolvedProviderModel)
			require.Equal(t, PlatformOpenAI, got.Platform)
			require.Equal(t, []int64{11, 22}, got.SchedulableAccountScope)
			require.Empty(t, got.RejectionReason)
		})
	}
}

func TestExplicitModelResolverRejections(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		entry       *ExplicitModelApprovalSnapshot
		wantErr     error
		wantReason  string
		catalogCall bool
	}{
		{name: "model not approved", profile: ProtocolProfileAnthropicMessagesV1, wantErr: ErrExplicitModelNotAllowed, wantReason: ExplicitModelRejectionNotAllowed, catalogCall: true},
		{name: "approved but unavailable", profile: ProtocolProfileOpenAIResponsesV1, entry: &ExplicitModelApprovalSnapshot{EntryID: "x", LogicalModel: "company-coder", Platform: PlatformOpenAI, ResolvedProviderModel: "gpt-5.6-codex"}, wantErr: ErrExplicitModelUnavailable, wantReason: ExplicitModelRejectionUnavailable, catalogCall: true},
		{name: "catalog cannot replace logical model", profile: ProtocolProfileAnthropicMessagesV1, entry: &ExplicitModelApprovalSnapshot{EntryID: "x", LogicalModel: "other-model", Platform: PlatformOpenAI, ResolvedProviderModel: "gpt-5.6-codex", SchedulableAccountScope: []int64{1}}, wantErr: ErrExplicitModelNotAllowed, wantReason: ExplicitModelRejectionNotAllowed, catalogCall: true},
		{name: "unknown profile fails closed", profile: "future-profile-v9", wantErr: ErrExplicitModelNotAllowed, wantReason: ExplicitModelRejectionNotAllowed, catalogCall: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := &explicitModelCatalogStub{find: func(ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error) { return tt.entry, nil }}
			got, err := NewExplicitModelResolver(catalog).Resolve(context.Background(), explicitModelInput(tt.profile, "company-coder"))
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, "company-coder", got.RequestedLogicalModel)
			require.Equal(t, tt.wantReason, got.RejectionReason)
			if tt.catalogCall {
				require.Equal(t, 1, catalog.calls)
			} else {
				require.Zero(t, catalog.calls)
			}
		})
	}
}

type explicitGroupReaderStub struct{ group *Group }

func (s explicitGroupReaderStub) GetByID(context.Context, int64) (*Group, error) { return s.group, nil }

type explicitAccountReaderStub struct{ accounts []Account }

func (s explicitAccountReaderStub) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return append([]Account(nil), s.accounts...), nil
}

type explicitChannelReaderStub struct {
	channel *Channel
	mapping ChannelMappingResult
}

func (s explicitChannelReaderStub) GetChannelForGroup(context.Context, int64) (*Channel, error) {
	return s.channel, nil
}
func (s explicitChannelReaderStub) ResolveChannelMapping(context.Context, int64, string) ChannelMappingResult {
	return s.mapping
}

func TestExplicitModelCatalogAdapterUsesExactCurrentConfiguration(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: true}
	channel := &Channel{
		ID: 9, Status: StatusActive,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"company-coder": "gpt-5.6-codex"}},
	}
	accounts := []Account{
		{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true},
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: false},
		{ID: 4, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.6-codex": "other-model"}}},
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"company-coder": "gpt-5.6-codex"}}},
		{ID: 5, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.6-codex": "gpt-5.6-codex"}}},
	}
	adapter := NewExplicitModelCatalogAdapter(
		explicitGroupReaderStub{group: group},
		explicitAccountReaderStub{accounts: accounts},
		explicitChannelReaderStub{channel: channel, mapping: ChannelMappingResult{Mapped: true, MappedModel: "gpt-5.6-codex", ChannelID: 9}},
	)

	entry, err := adapter.FindApprovedExplicitModel(context.Background(), explicitModelInput(ProtocolProfileAnthropicMessagesV1, "company-coder"))
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, "company-coder", entry.LogicalModel)
	require.Equal(t, "gpt-5.6-codex", entry.ResolvedProviderModel)
	require.Equal(t, []int64{2, 3}, entry.SchedulableAccountScope)
}

func TestExplicitModelCatalogAdapterDistinguishesNotAllowedAndUnavailable(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	base := explicitModelInput(ProtocolProfileOpenAIResponsesV1, "company-claude")

	t.Run("not allowed without exact channel approval", func(t *testing.T) {
		adapter := NewExplicitModelCatalogAdapter(
			explicitGroupReaderStub{group: group}, explicitAccountReaderStub{},
			explicitChannelReaderStub{channel: &Channel{ID: 8, Status: StatusActive}},
		)
		entry, err := adapter.FindApprovedExplicitModel(context.Background(), base)
		require.NoError(t, err)
		require.Nil(t, entry)
	})

	t.Run("approved entry can have no schedulable scope", func(t *testing.T) {
		channel := &Channel{ID: 8, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"company-claude"}}}}
		adapter := NewExplicitModelCatalogAdapter(
			explicitGroupReaderStub{group: group}, explicitAccountReaderStub{},
			explicitChannelReaderStub{channel: channel, mapping: ChannelMappingResult{MappedModel: "company-claude", ChannelID: 8}},
		)
		entry, err := adapter.FindApprovedExplicitModel(context.Background(), base)
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Empty(t, entry.SchedulableAccountScope)
		_, resolveErr := NewExplicitModelResolver(adapter).Resolve(context.Background(), base)
		require.True(t, errors.Is(resolveErr, ErrExplicitModelUnavailable))
	})
}

func TestExplicitModelResolutionAccountScopeAllowsSameModelFailoverOnly(t *testing.T) {
	resolution := ExplicitModelResolution{
		RequestedLogicalModel: "company-coder", ResolvedProviderModel: "gpt-5.6-codex",
		SchedulableAccountScope: []int64{10, 20},
	}
	ctx := WithExplicitModelResolution(context.Background(), resolution)
	accounts := []Account{{ID: 10}, {ID: 20}, {ID: 30}}
	require.Equal(t, []Account{{ID: 10}, {ID: 20}}, filterAccountsForExplicitModelResolution(ctx, accounts))

	mapping := ApplyExplicitModelResolution(ctx, "company-coder", ChannelMappingResult{MappedModel: "legacy-fallback", Mapped: true})
	require.Equal(t, "gpt-5.6-codex", mapping.MappedModel)
	require.True(t, mapping.Mapped)
}
