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

type clientProfilesFixture struct {
	ContractVersion    string                 `json:"contract_version"`
	UserAgentAdmission bool                   `json:"user_agent_admission"`
	Profiles           []clientProfileFixture `json:"profiles"`
}

type clientProfileFixture struct {
	ID             string            `json:"id"`
	Client         string            `json:"client"`
	ProfileVersion string            `json:"profile_version"`
	ReusedProfile  string            `json:"reused_profile,omitempty"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Transport      string            `json:"transport"`
	Headers        map[string]string `json:"headers"`
	Body           json.RawMessage   `json:"body"`
	Decision       string            `json:"decision"`
}

func loadClientProfiles(t *testing.T) clientProfilesFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/client_profiles_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture clientProfilesFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("strict client profile fixture decode failed: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("client profile fixture has trailing content: %v", err)
	}
	return fixture
}

func TestClientProfilesFreezeMethodPathTransportHeadersAndBody(t *testing.T) {
	fixture := loadClientProfiles(t)
	if fixture.ContractVersion != "core-gateway-clientProfile-client-profiles-v1" || fixture.UserAgentAdmission {
		t.Fatalf("unsafe client profile fixture header: %+v", fixture)
	}

	clients := map[string]bool{}
	ids := map[string]bool{}
	allowedCompact := map[string]bool{}
	for _, profile := range fixture.Profiles {
		if profile.ID == "" || ids[profile.ID] {
			t.Fatalf("empty or duplicate profile id %q", profile.ID)
		}
		ids[profile.ID] = true
		clients[profile.Client] = true
		if profile.Method != strings.ToUpper(profile.Method) || profile.Path == "" || path.Clean(profile.Path) != profile.Path {
			t.Fatalf("non-exact method/path in %+v", profile)
		}
		if profile.Transport != "http" && profile.Transport != "sse" && profile.Transport != "websocket" {
			t.Fatalf("unknown transport in %+v", profile)
		}
		if profile.Decision == "allow" {
			if profile.Headers["content-type"] != "application/json" || len(profile.Body) == 0 || string(profile.Body) == "null" {
				t.Fatalf("allowed fixture lacks exact headers/body: %s", profile.ID)
			}
			if profile.ProfileVersion == "anthropic-messages-v1" && profile.Headers["anthropic-version"] == "" {
				t.Fatalf("anthropic fixture lacks protocol version: %s", profile.ID)
			}
		}
		if profile.Client == "opencode" && profile.Decision == "allow" && profile.ReusedProfile != profile.ProfileVersion {
			t.Fatalf("OpenCode gained a non-reused profile: %+v", profile)
		}
		if strings.HasSuffix(profile.Path, "/responses/compact") && profile.Decision == "allow" {
			allowedCompact[profile.Path+"|"+profile.Transport] = true
		}
	}

	gotClients := make([]string, 0, len(clients))
	for client := range clients {
		gotClients = append(gotClients, client)
	}
	sort.Strings(gotClients)
	if strings.Join(gotClients, ",") != "claude_code,codex,opencode" {
		t.Fatalf("client fixture coverage = %v", gotClients)
	}
	for _, tuple := range []string{"/v1/responses/compact|http", "/responses/compact|sse"} {
		if !allowedCompact[tuple] {
			t.Fatalf("verified compact tuple missing: %s", tuple)
		}
	}
	if !ids["opencode_user_agent_cannot_widen_chat"] || !ids["codex_websocket_cannot_widen_responses"] {
		t.Fatal("default-deny client fixtures are incomplete")
	}
}
