package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ProtocolProfileAnthropicMessagesV1 = "anthropic-messages-v1"
	ProtocolProfileOpenAIResponsesV1   = "openai-responses-v1"

	ExplicitModelRejectionNotAllowed  = "model_not_allowed"
	ExplicitModelRejectionUnavailable = "model_unavailable"
)

var (
	ErrExplicitModelNotAllowed = infraerrors.Forbidden(
		"GATEWAY_MODEL_NOT_ALLOWED",
		"requested logical model is not explicitly approved",
	)
	ErrExplicitModelUnavailable = infraerrors.ServiceUnavailable(
		"GATEWAY_MODEL_UNAVAILABLE",
		"requested logical model has no schedulable account",
	)
)

// ExplicitModelAuthenticatedUser is the existing Sub2API user principal after
// API Key authentication. It deliberately contains no API Key material.
type ExplicitModelAuthenticatedUser struct {
	ID     int64
	Status string
	Role   string
}

// ExplicitModelGroupAccessContext is the existing Group/access boundary carried
// by the authenticated Sub2API API Key. The Key secret and public value are not
// resolver inputs and therefore cannot select a platform or model.
type ExplicitModelGroupAccessContext struct {
	GroupID int64
}

type ExplicitModelResolveInput struct {
	AuthenticatedUser      ExplicitModelAuthenticatedUser
	Access                 ExplicitModelGroupAccessContext
	RequestedLogicalModel  string
	ProtocolProfileVersion string
}

// ExplicitModelApprovalSnapshot is a read-only explicit-model contract adapter view over the
// current Group/channel/account model configuration. A future catalog adapter
// can replace this implementation without changing resolver callers.
type ExplicitModelApprovalSnapshot struct {
	EntryID                 string
	GroupID                 int64
	ChannelID               int64
	LogicalModel            string
	Platform                string
	ResolvedProviderModel   string
	SchedulableAccountScope []int64
	ConfigurationVersion    int64
}

type ExplicitModelResolution struct {
	RequestedLogicalModel   string
	ApprovedModel           ExplicitModelApprovalSnapshot
	Platform                string
	ResolvedProviderModel   string
	SchedulableAccountScope []int64
	ProtocolProfileVersion  string
	RejectionReason         string
}

type ExplicitModelCatalog interface {
	FindApprovedExplicitModel(ctx context.Context, input ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error)
}

type ExplicitModelResolver struct {
	catalog ExplicitModelCatalog
}

func NewExplicitModelResolver(catalog ExplicitModelCatalog) *ExplicitModelResolver {
	return &ExplicitModelResolver{catalog: catalog}
}

func (r *ExplicitModelResolver) Resolve(ctx context.Context, input ExplicitModelResolveInput) (ExplicitModelResolution, error) {
	requested := strings.TrimSpace(input.RequestedLogicalModel)
	base := ExplicitModelResolution{
		RequestedLogicalModel:  requested,
		ProtocolProfileVersion: strings.TrimSpace(input.ProtocolProfileVersion),
	}
	if r == nil || r.catalog == nil || input.AuthenticatedUser.ID <= 0 ||
		input.AuthenticatedUser.Status != StatusActive || input.Access.GroupID <= 0 || requested == "" ||
		!isSupportedExplicitModelProfile(base.ProtocolProfileVersion) {
		base.RejectionReason = ExplicitModelRejectionNotAllowed
		return base, ErrExplicitModelNotAllowed
	}

	input.RequestedLogicalModel = requested
	input.ProtocolProfileVersion = base.ProtocolProfileVersion
	entry, err := r.catalog.FindApprovedExplicitModel(ctx, input)
	if err != nil {
		base.RejectionReason = ExplicitModelRejectionUnavailable
		return base, err
	}
	if entry == nil || entry.LogicalModel != requested || strings.TrimSpace(entry.Platform) == "" ||
		strings.TrimSpace(entry.ResolvedProviderModel) == "" {
		base.RejectionReason = ExplicitModelRejectionNotAllowed
		return base, ErrExplicitModelNotAllowed
	}
	if !profileAllowsExplicitModelPlatform(base.ProtocolProfileVersion, entry.Platform) {
		base.RejectionReason = ExplicitModelRejectionNotAllowed
		return base, ErrExplicitModelNotAllowed
	}

	scope := normalizedPositiveIDs(entry.SchedulableAccountScope)
	if len(scope) == 0 {
		base.RejectionReason = ExplicitModelRejectionUnavailable
		return base, ErrExplicitModelUnavailable
	}

	approved := *entry
	approved.SchedulableAccountScope = append([]int64(nil), scope...)
	base.ApprovedModel = approved
	base.Platform = approved.Platform
	base.ResolvedProviderModel = approved.ResolvedProviderModel
	base.SchedulableAccountScope = append([]int64(nil), scope...)
	return base, nil
}

func isSupportedExplicitModelProfile(profile string) bool {
	switch profile {
	case ProtocolProfileAnthropicMessagesV1, ProtocolProfileOpenAIResponsesV1:
		return true
	default:
		return false
	}
}

// explicit-model contract verifies protocol-profile compatibility independently of provider
// platform. Existing provider Groups can serve either of the two frozen
// HTTP/SSE profiles through the repository's existing protocol bridges.
func profileAllowsExplicitModelPlatform(profile, platform string) bool {
	if !isSupportedExplicitModelProfile(profile) {
		return false
	}
	switch platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok:
		return true
	default:
		return false
	}
}

func normalizedPositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

type explicitModelGroupReader interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
}

type explicitModelAccountReader interface {
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
}

type explicitModelChannelReader interface {
	GetChannelForGroup(ctx context.Context, groupID int64) (*Channel, error)
	ResolveChannelMapping(ctx context.Context, groupID int64, model string) ChannelMappingResult
}

// ExplicitModelCatalogAdapter is the minimal explicit-model contract bridge over the existing
// Group, channel and account configuration. It creates no catalog table and
// performs no credential validation.
type ExplicitModelCatalogAdapter struct {
	groups   explicitModelGroupReader
	accounts explicitModelAccountReader
	channels explicitModelChannelReader
}

func NewExplicitModelCatalogAdapter(
	groups explicitModelGroupReader,
	accounts explicitModelAccountReader,
	channels explicitModelChannelReader,
) *ExplicitModelCatalogAdapter {
	return &ExplicitModelCatalogAdapter{groups: groups, accounts: accounts, channels: channels}
}

func (a *ExplicitModelCatalogAdapter) FindApprovedExplicitModel(ctx context.Context, input ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error) {
	if a == nil || a.groups == nil || a.accounts == nil || a.channels == nil {
		return nil, ErrExplicitModelUnavailable
	}
	group, err := a.groups.GetByID(ctx, input.Access.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil || group.ID != input.Access.GroupID || !group.IsActive() ||
		!profileAllowsExplicitModelPlatform(input.ProtocolProfileVersion, group.Platform) {
		return nil, nil
	}
	if input.ProtocolProfileVersion == ProtocolProfileAnthropicMessagesV1 &&
		group.Platform == PlatformOpenAI && !group.AllowMessagesDispatch {
		return nil, nil
	}

	channel, err := a.channels.GetChannelForGroup(ctx, group.ID)
	if err != nil {
		return nil, err
	}
	if channel == nil || !channel.IsActive() {
		return nil, nil
	}
	if !channelExplicitlyApprovesModel(channel, group.Platform, input.RequestedLogicalModel) {
		return nil, nil
	}

	mapping := a.channels.ResolveChannelMapping(ctx, group.ID, input.RequestedLogicalModel)
	providerModel := strings.TrimSpace(mapping.MappedModel)
	if providerModel == "" {
		return nil, nil
	}

	accounts, err := a.accounts.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, group.Platform)
	if err != nil {
		return nil, err
	}
	scope := make([]int64, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != group.Platform || !account.IsSchedulable() {
			continue
		}
		// Account failover is permitted only when it preserves the one provider
		// model approved by the channel snapshot. A second account-level rewrite
		// would be a silent cross-model substitution and is therefore excluded.
		if account.GetMappedModel(providerModel) != providerModel {
			continue
		}
		// Existing schedulers still test the requested logical model before the
		// handler applies the approved channel mapping. Keep only accounts that
		// can pass that check, and reject any account-level rewrite that points
		// the logical name at a different provider model.
		if !account.IsModelSupported(input.RequestedLogicalModel) {
			continue
		}
		mappedFromLogical := account.GetMappedModel(input.RequestedLogicalModel)
		if mappedFromLogical != input.RequestedLogicalModel && mappedFromLogical != providerModel {
			continue
		}
		scope = append(scope, account.ID)
	}
	scope = normalizedPositiveIDs(scope)

	version := group.UpdatedAt.UTC().UnixMilli()
	if channelVersion := channel.UpdatedAt.UTC().UnixMilli(); channelVersion > version {
		version = channelVersion
	}
	if version <= 0 {
		version = 1
	}
	return &ExplicitModelApprovalSnapshot{
		EntryID:                 fmt.Sprintf("group-%d-channel-%d-model-%s", group.ID, channel.ID, input.RequestedLogicalModel),
		GroupID:                 group.ID,
		ChannelID:               channel.ID,
		LogicalModel:            input.RequestedLogicalModel,
		Platform:                group.Platform,
		ResolvedProviderModel:   providerModel,
		SchedulableAccountScope: scope,
		ConfigurationVersion:    version,
	}, nil
}

func channelExplicitlyApprovesModel(channel *Channel, platform, requested string) bool {
	if channel == nil || requested == "" {
		return false
	}
	for _, model := range channel.SupportedModels() {
		if model.Platform == platform && model.Name == requested {
			return true
		}
	}
	return false
}

type explicitModelResolutionContextKey struct{}

type explicitModelResolutionContextValue struct {
	resolution ExplicitModelResolution
	allowed    map[int64]struct{}
}

func WithExplicitModelResolution(ctx context.Context, resolution ExplicitModelResolution) context.Context {
	allowed := make(map[int64]struct{}, len(resolution.SchedulableAccountScope))
	for _, id := range resolution.SchedulableAccountScope {
		if id > 0 {
			allowed[id] = struct{}{}
		}
	}
	return context.WithValue(ctx, explicitModelResolutionContextKey{}, explicitModelResolutionContextValue{
		resolution: resolution,
		allowed:    allowed,
	})
}

func ExplicitModelResolutionFromContext(ctx context.Context) (ExplicitModelResolution, bool) {
	if ctx == nil {
		return ExplicitModelResolution{}, false
	}
	value, ok := ctx.Value(explicitModelResolutionContextKey{}).(explicitModelResolutionContextValue)
	if !ok {
		return ExplicitModelResolution{}, false
	}
	resolution := value.resolution
	resolution.SchedulableAccountScope = append([]int64(nil), value.resolution.SchedulableAccountScope...)
	resolution.ApprovedModel.SchedulableAccountScope = append([]int64(nil), value.resolution.ApprovedModel.SchedulableAccountScope...)
	return resolution, true
}

func ApplyExplicitModelResolution(ctx context.Context, requested string, mapping ChannelMappingResult) ChannelMappingResult {
	resolution, ok := ExplicitModelResolutionFromContext(ctx)
	if !ok || resolution.RequestedLogicalModel != requested || resolution.ResolvedProviderModel == "" {
		return mapping
	}
	mapping.MappedModel = resolution.ResolvedProviderModel
	mapping.Mapped = resolution.ResolvedProviderModel != requested
	return mapping
}

func filterAccountsForExplicitModelResolution(ctx context.Context, accounts []Account) []Account {
	value, ok := ctx.Value(explicitModelResolutionContextKey{}).(explicitModelResolutionContextValue)
	if !ok {
		return accounts
	}
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if _, allowed := value.allowed[accounts[i].ID]; allowed {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}

func explicitModelResolutionAllowsAccount(ctx context.Context, accountID int64) bool {
	value, ok := ctx.Value(explicitModelResolutionContextKey{}).(explicitModelResolutionContextValue)
	if !ok {
		return true
	}
	_, allowed := value.allowed[accountID]
	return allowed
}
