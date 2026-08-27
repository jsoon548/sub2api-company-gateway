package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
)

const coreGatewayErrorContractVersion = "core-gateway-contracts-v1"

type coreGatewayProtocolErrorFixture struct {
	ContractVersion string                         `json:"contract_version"`
	RequestIDPolicy coreGatewayRequestIDPolicy     `json:"request_id_policy"`
	Errors          []coreGatewayProtocolErrorCase `json:"errors"`
}

type coreGatewayRequestIDPolicy struct {
	Header                     string   `json:"header"`
	Owner                      string   `json:"owner"`
	Generation                 string   `json:"generation"`
	ClientHeaderInputHandling  string   `json:"client_header_input_handling"`
	ClientHeaderCanOverride    bool     `json:"client_header_can_override"`
	ResponseHeaderRequired     bool     `json:"response_header_required"`
	GeneratedErrorBodyRequired bool     `json:"generated_error_body_required"`
	ExampleGatewayRequestID    string   `json:"example_gateway_request_id"`
	SeparateIdentifiers        []string `json:"separate_identifiers"`
	LegacyUsageRequestIDReused bool     `json:"legacy_usage_request_id_reused"`
}

type coreGatewayProtocolErrorCase struct {
	ID                     string          `json:"id"`
	Scenario               string          `json:"scenario"`
	Protocol               string          `json:"protocol"`
	Code                   string          `json:"code"`
	HTTPStatus             int             `json:"http_status"`
	UpstreamCallCount      int             `json:"upstream_call_count"`
	ResponseHeaderRequired bool            `json:"response_header_required"`
	Body                   json.RawMessage `json:"body"`
}

type anthropicGatewayErrorBody struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
	GatewayRequestID string `json:"gateway_request_id"`
}

type openAIGatewayErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	GatewayRequestID string `json:"gateway_request_id"`
}

func strictJSONDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}

func loadCoreGatewayProtocolErrors(t *testing.T) ([]byte, coreGatewayProtocolErrorFixture) {
	t.Helper()
	raw, err := os.ReadFile("testdata/core_gateway_protocol_errors_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture coreGatewayProtocolErrorFixture
	if err := strictJSONDecode(raw, &fixture); err != nil {
		t.Fatalf("strict fixture decode failed: %v", err)
	}
	return raw, fixture
}

func TestCoreGatewayProtocolFixtureRejectsUnknownFields(t *testing.T) {
	raw, fixture := loadCoreGatewayProtocolErrors(t)
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected_field"] = true
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var decoded coreGatewayProtocolErrorFixture
	if err := strictJSONDecode(mutated, &decoded); err == nil {
		t.Fatal("fixture with unknown top-level field was accepted")
	}

	var body map[string]any
	if err := json.Unmarshal(fixture.Errors[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	body["unexpected_field"] = true
	mutatedBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var anthropicBody anthropicGatewayErrorBody
	if err := strictJSONDecode(mutatedBody, &anthropicBody); err == nil {
		t.Fatal("protocol body with unknown field was accepted")
	}
}

func TestGatewayRequestIDOwnershipIsFrozen(t *testing.T) {
	_, fixture := loadCoreGatewayProtocolErrors(t)
	policy := fixture.RequestIDPolicy
	if fixture.ContractVersion != coreGatewayErrorContractVersion {
		t.Fatalf("contract version = %q", fixture.ContractVersion)
	}
	if policy.Header != "X-Gateway-Request-ID" || policy.Owner != "gateway" || policy.Generation != "unique_per_employee_api_request" {
		t.Fatalf("unexpected Gateway Request ID ownership: %+v", policy)
	}
	if policy.ClientHeaderInputHandling != "ignore_and_replace" || policy.ClientHeaderCanOverride {
		t.Fatalf("client header could override Gateway ID: %+v", policy)
	}
	if !policy.ResponseHeaderRequired || !policy.GeneratedErrorBodyRequired || policy.LegacyUsageRequestIDReused {
		t.Fatalf("Gateway ID propagation/separation changed: %+v", policy)
	}
	got := append([]string(nil), policy.SeparateIdentifiers...)
	sort.Strings(got)
	want := []string{"client_request_id", "upstream_request_id", "usage_logs.request_id"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("separate identifiers = %v, want %v", got, want)
	}
}

func TestNativeProtocolErrorsArePairedAndPreUpstream(t *testing.T) {
	_, fixture := loadCoreGatewayProtocolErrors(t)
	wantStatus := map[string]int{
		"audit_unavailable":                      503,
		"entry_not_allowed":                      403,
		"authentication_failed":                  401,
		"account_disabled":                       403,
		"explicit_model_not_allowed":             403,
		"explicit_model_unschedulable":           503,
		"quota_exhausted_or_new_session_blocked": 429,
		"auto_not_enabled_or_not_in_pilot":       403,
	}
	wantCode := map[string]string{
		"audit_unavailable":                      "gateway_audit_unavailable",
		"entry_not_allowed":                      "gateway_entry_not_allowed",
		"authentication_failed":                  "gateway_authentication_failed",
		"account_disabled":                       "gateway_account_disabled",
		"explicit_model_not_allowed":             "gateway_model_not_allowed",
		"explicit_model_unschedulable":           "gateway_model_unavailable",
		"quota_exhausted_or_new_session_blocked": "gateway_quota_exhausted",
		"auto_not_enabled_or_not_in_pilot":       "gateway_auto_unavailable",
	}
	pairs := map[string]map[string]bool{}
	ids := map[string]bool{}
	for _, item := range fixture.Errors {
		if item.ID == "" || ids[item.ID] {
			t.Fatalf("empty or duplicate error id %q", item.ID)
		}
		ids[item.ID] = true
		status, ok := wantStatus[item.Scenario]
		if !ok || item.HTTPStatus != status {
			t.Fatalf("scenario %s status = %d, want %d", item.Scenario, item.HTTPStatus, status)
		}
		if item.Code != wantCode[item.Scenario] {
			t.Fatalf("scenario %s code = %q, want %q", item.Scenario, item.Code, wantCode[item.Scenario])
		}
		if item.UpstreamCallCount != 0 {
			t.Fatalf("pre-upstream error %s allows %d upstream calls", item.ID, item.UpstreamCallCount)
		}
		if !item.ResponseHeaderRequired {
			t.Fatalf("error %s omits response Gateway ID header", item.ID)
		}
		if strings.Contains(strings.ToLower(item.Code), "employee_key") {
			t.Fatalf("error %s depends on deferred Employee Gateway Key semantics", item.ID)
		}
		pairs[item.Scenario] = mapProtocol(pairs[item.Scenario], item.Protocol)

		switch item.Protocol {
		case "anthropic":
			var body anthropicGatewayErrorBody
			if err := strictJSONDecode(item.Body, &body); err != nil {
				t.Fatalf("Anthropic body %s is not strict: %v", item.ID, err)
			}
			if body.Type != "error" || body.Error.Code != item.Code || body.Error.Type == "" || body.Error.Message == "" || body.GatewayRequestID != fixture.RequestIDPolicy.ExampleGatewayRequestID {
				t.Fatalf("Anthropic envelope %s changed: %+v", item.ID, body)
			}
		case "openai":
			var body openAIGatewayErrorBody
			if err := strictJSONDecode(item.Body, &body); err != nil {
				t.Fatalf("OpenAI body %s is not strict: %v", item.ID, err)
			}
			if body.Error.Code != item.Code || body.Error.Type == "" || body.Error.Message == "" || body.GatewayRequestID != fixture.RequestIDPolicy.ExampleGatewayRequestID {
				t.Fatalf("OpenAI envelope %s changed: %+v", item.ID, body)
			}
		default:
			t.Fatalf("error %s has undefined protocol %q", item.ID, item.Protocol)
		}
	}
	for scenario := range wantStatus {
		if !pairs[scenario]["anthropic"] || !pairs[scenario]["openai"] || len(pairs[scenario]) != 2 {
			t.Fatalf("scenario %s does not have exactly one native pair: %v", scenario, pairs[scenario])
		}
	}
}

func mapProtocol(existing map[string]bool, protocol string) map[string]bool {
	if existing == nil {
		existing = map[string]bool{}
	}
	if existing[protocol] {
		existing["duplicate:"+protocol] = true
	}
	existing[protocol] = true
	return existing
}
