package routes

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
)

const coreGatewayContractVersion = "core-gateway-contracts-v1"

type coreGatewayEntryFixture struct {
	ContractVersion        string                            `json:"contract_version"`
	DefaultContentDecision string                            `json:"default_content_decision"`
	AdmissionDiscriminator string                            `json:"admission_discriminator"`
	UserAgentAdmission     bool                              `json:"user_agent_admission"`
	Rows                   []coreGatewayEntryRow             `json:"rows"`
	ClientCompatibility    []coreGatewayCompatibilityBinding `json:"client_compatibility"`
}

type coreGatewayEntryRow struct {
	ID               string   `json:"id"`
	Profile          string   `json:"profile"`
	ProfileVersion   string   `json:"profile_version"`
	Protocol         string   `json:"protocol"`
	Method           string   `json:"method"`
	Path             string   `json:"path"`
	Transport        string   `json:"transport"`
	Decision         string   `json:"decision"`
	EnabledByDefault bool     `json:"enabled_by_default"`
	RequiredGates    []string `json:"required_gates"`
	AuditScope       string   `json:"audit_scope"`
}

type coreGatewayCompatibilityBinding struct {
	ID                   string `json:"id"`
	Client               string `json:"client"`
	ReusedProfileVersion string `json:"reused_profile_version"`
	AdmissionBasis       string `json:"admission_basis"`
	RequiredGate         string `json:"required_gate"`
}

func decodeCoreGatewayEntryFixture(raw []byte) (coreGatewayEntryFixture, error) {
	var fixture coreGatewayEntryFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return fixture, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fixture, err
	}
	return fixture, nil
}

func loadCoreGatewayEntryFixture(t *testing.T) ([]byte, coreGatewayEntryFixture) {
	t.Helper()
	raw, err := os.ReadFile("testdata/core_gateway_entry_profiles_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := decodeCoreGatewayEntryFixture(raw)
	if err != nil {
		t.Fatalf("strict fixture decode failed: %v", err)
	}
	return raw, fixture
}

func TestCoreGatewayEntryFixtureRejectsUnknownFields(t *testing.T) {
	raw, _ := loadCoreGatewayEntryFixture(t)
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected_field"] = true
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCoreGatewayEntryFixture(mutated); err == nil {
		t.Fatal("fixture with unknown field was accepted")
	}
}

func TestCoreGatewayEntryProfilesAreExactAndDefaultDeny(t *testing.T) {
	_, fixture := loadCoreGatewayEntryFixture(t)
	if fixture.ContractVersion != coreGatewayContractVersion {
		t.Fatalf("contract version = %q", fixture.ContractVersion)
	}
	if fixture.DefaultContentDecision != "deny" {
		t.Fatalf("default content decision = %q", fixture.DefaultContentDecision)
	}
	if fixture.AdmissionDiscriminator != "method_path_transport_profile_version" {
		t.Fatalf("admission discriminator = %q", fixture.AdmissionDiscriminator)
	}
	if fixture.UserAgentAdmission {
		t.Fatal("User-Agent must not participate in admission")
	}

	ids := map[string]bool{}
	tuples := map[string]string{}
	rowsByID := map[string]coreGatewayEntryRow{}
	allowedProfileVersions := map[string]bool{
		"anthropic-messages-v1": true,
		"openai-responses-v1":   true,
	}
	for _, row := range fixture.Rows {
		if row.ID == "" || ids[row.ID] {
			t.Fatalf("empty or duplicate row id %q", row.ID)
		}
		ids[row.ID] = true
		rowsByID[row.ID] = row
		if row.Path == "" || !strings.HasPrefix(row.Path, "/") || path.Clean(row.Path) != row.Path {
			t.Fatalf("row %s has non-canonical path %q", row.ID, row.Path)
		}
		if strings.ContainsAny(row.Path, "*{}?#") || strings.Contains(row.Path, "//") || strings.Contains(row.Path, ":") {
			t.Fatalf("row %s uses a wildcard, template, query, fragment, or scheme in %q", row.ID, row.Path)
		}
		if row.Method != strings.ToUpper(row.Method) {
			t.Fatalf("row %s method is not uppercase: %q", row.ID, row.Method)
		}
		tuple := strings.Join([]string{row.Method, row.Path, row.Transport, row.ProfileVersion}, "|")
		if prior, ok := tuples[tuple]; ok {
			t.Fatalf("duplicate tuple %s in %s and %s", tuple, prior, row.ID)
		}
		tuples[tuple] = row.ID

		switch row.Decision {
		case "allow_content":
			if row.AuditScope != "full_content" {
				t.Fatalf("content row %s is not fully audited", row.ID)
			}
			if !allowedProfileVersions[row.ProfileVersion] {
				t.Fatalf("content row %s uses unapproved profile %q", row.ID, row.ProfileVersion)
			}
		case "metadata_only":
			if row.AuditScope != "safe_metadata" {
				t.Fatalf("metadata row %s has audit scope %q", row.ID, row.AuditScope)
			}
		case "deny":
			if row.ProfileVersion != "core-gateway-default-deny-v1" || row.AuditScope != "safe_metadata" {
				t.Fatalf("deny row %s is not in the default-deny profile", row.ID)
			}
		default:
			t.Fatalf("row %s has undefined decision %q", row.ID, row.Decision)
		}
	}

	required := map[string]struct {
		method, endpoint, transport, decision string
	}{
		"claude_messages_http":              {"POST", "/v1/messages", "http", "allow_content"},
		"claude_messages_sse":               {"POST", "/v1/messages", "sse", "allow_content"},
		"claude_count_tokens_http":          {"POST", "/v1/messages/count_tokens", "http", "allow_content"},
		"claude_models_metadata":            {"GET", "/v1/models", "http", "metadata_only"},
		"claude_root_probe_metadata":        {"HEAD", "/", "http", "metadata_only"},
		"codex_v1_responses_http":           {"POST", "/v1/responses", "http", "allow_content"},
		"codex_v1_responses_sse":            {"POST", "/v1/responses", "sse", "allow_content"},
		"codex_responses_http":              {"POST", "/responses", "http", "allow_content"},
		"codex_responses_sse":               {"POST", "/responses", "sse", "allow_content"},
		"codex_models_metadata":             {"GET", "/v1/models", "http", "metadata_only"},
		"codex_root_probe_metadata":         {"HEAD", "/", "http", "metadata_only"},
		"deny_chat_completions_http":        {"POST", "/v1/chat/completions", "http", "deny"},
		"deny_chat_completions_sse":         {"POST", "/v1/chat/completions", "sse", "deny"},
		"deny_v1_responses_websocket":       {"GET", "/v1/responses", "websocket", "deny"},
		"deny_responses_websocket":          {"GET", "/responses", "websocket", "deny"},
		"deny_unknown_v1_responses_subpath": {"POST", "/v1/responses/unknown", "http", "deny"},
		"deny_unknown_messages_subpath":     {"POST", "/v1/messages/unknown", "http", "deny"},
		"deny_backend_alias_http":           {"POST", "/backend-api/codex/responses", "http", "deny"},
	}
	for id, want := range required {
		row, ok := rowsByID[id]
		if !ok {
			t.Fatalf("required entry %s is missing", id)
		}
		if row.Method != want.method || row.Path != want.endpoint || row.Transport != want.transport || row.Decision != want.decision {
			t.Fatalf("entry %s changed: %+v", id, row)
		}
	}

	for _, id := range []string{"codex_v1_compact_http", "codex_v1_compact_sse", "codex_compact_http", "codex_compact_sse"} {
		row := rowsByID[id]
		if row.EnabledByDefault || len(row.RequiredGates) != 1 || row.RequiredGates[0] != "compact_protocol_verified" {
			t.Fatalf("compact entry %s is not exactly gated", id)
		}
	}
}

func TestOpenCodeOnlyReusesVerifiedProtocolProfiles(t *testing.T) {
	_, fixture := loadCoreGatewayEntryFixture(t)
	if len(fixture.ClientCompatibility) != 2 {
		t.Fatalf("compatibility bindings = %d, want 2", len(fixture.ClientCompatibility))
	}
	profiles := make([]string, 0, len(fixture.ClientCompatibility))
	for _, binding := range fixture.ClientCompatibility {
		if binding.Client != "opencode" || binding.AdmissionBasis != "verified_protocol_profile" || binding.RequiredGate != "reused_profile_protocol_verified" {
			t.Fatalf("unsafe OpenCode binding: %+v", binding)
		}
		profiles = append(profiles, binding.ReusedProfileVersion)
	}
	sort.Strings(profiles)
	want := []string{"anthropic-messages-v1", "openai-responses-v1"}
	if strings.Join(profiles, ",") != strings.Join(want, ",") {
		t.Fatalf("OpenCode profiles = %v, want %v", profiles, want)
	}
}
