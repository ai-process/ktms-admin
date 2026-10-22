package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
	"github.com/ai-process/llm-proxy-admin/internal/config"
	pb "github.com/ai-process/llm-proxy-admin/internal/gen/ktms/v1"
)

func (f *fakeAdmin) GetTeamSpend(_ context.Context, r *pb.GetTeamSpendRequest, _ ...grpc.CallOption) (*pb.GetTeamSpendResponse, error) {
	return &pb.GetTeamSpendResponse{SpentNanousd: 40e9, LimitNanousd: 100e9, Metered: true, ResetsAt: timestamppb.New(time.Now().Add(time.Hour))}, nil
}

// tenantServer signs in boss@x.com as admin (and *@corp.com by wildcard),
// p@view.com as viewer, and three team users.
func tenantServer(t *testing.T) (http.Handler, *fakeAdmin) {
	t.Helper()
	admins, _ := auth.ParseAllowlist("boss@x.com,*@corp.com")
	viewers, _ := auth.ParseAllowlist("*@view.com")
	members := auth.ParseTeamMemberships("lead@corp.com:team-1:teamlead dev@corp.com:team-1:member ghost@corp.com::teamlead")
	h := auth.New(auth.Options{PublicURL: "https://admin.example.com", Admins: admins, Viewers: viewers, TeamMemberships: members,
		Signer: auth.NewSigner("0123456789abcdef0123456789abcdef"), SessionTTL: time.Hour})
	fa := &fakeAdmin{
		teams: map[string]*pb.Team{"team-1": {Id: "team-1", OrgId: "org-1", Name: "search", MonthlyBudgetUsd: 100}},
		keys: []*pb.APIKeyInfo{
			{Name: "search-service", TeamId: "team-1", OrgId: "org-1", CreatedAt: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))},
			{Name: "billing-service"},
			{Name: "ops-admin", Scopes: []string{"admin"}},
		},
	}
	s := &Server{cfg: &config.Config{PublicURL: "https://admin.example.com"}, auth: h, admin: fa, data: &fakeData{}}
	s.priceFetch = s.fetchPrices
	return s.Handler(), fa
}

// Team users must not read any tenant's data but their own.
func TestTeamUsersCannotReadGlobalData(t *testing.T) {
	h, _ := tenantServer(t)
	dev := sessionCookie(t, "dev@corp.com")
	for _, path := range []string{"/api/proxy/config", "/api/proxy/nodes", "/api/proxy/node-keys", "/api/proxy/user-rate-limits", "/api/proxy/organizations"} {
		if w := call(h, "GET", path, "", dev, nil); w.Code != http.StatusForbidden {
			t.Errorf("member GET %s = %d, want 403", path, w.Code)
		}
	}
	if w := call(h, "GET", "/api/proxy/config", "", sessionCookie(t, "p@view.com"), nil); w.Code != http.StatusOK {
		t.Errorf("viewer GET config = %d, want 200", w.Code)
	}
}

// Playground and judge run on the console's own key, outside team limits.
func TestTeamLeadCannotUsePlaygroundOrJudge(t *testing.T) {
	h, _ := tenantServer(t)
	lead := sessionCookie(t, "lead@corp.com")
	for _, path := range []string{"/api/proxy/playground", "/api/proxy/playground/stream", "/api/proxy/judge"} {
		if w := call(h, "POST", path, `{}`, lead, csrf); w.Code != http.StatusForbidden {
			t.Errorf("team lead POST %s = %d, want 403", path, w.Code)
		}
	}
}

func TestTeamKeyScoping(t *testing.T) {
	h, fa := tenantServer(t)
	lead := sessionCookie(t, "lead@corp.com")

	w := call(h, "GET", "/api/proxy/keys", "", lead, nil)
	var listed struct {
		Keys []struct {
			Name string `json:"name"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed.Keys) != 1 || listed.Keys[0].Name != "search-service" {
		t.Fatalf("lead's key list = %s (%v), want only their team's key", w.Body, err)
	}
	if w := call(h, "DELETE", "/api/proxy/keys/billing-service", "", lead, csrf); w.Code != http.StatusForbidden || fa.lastRevoke != "" {
		t.Fatalf("lead revoking an untenanted key = %d (revoked %q), want 403", w.Code, fa.lastRevoke)
	}
	if w := call(h, "POST", "/api/proxy/keys", `{"name":"new","scopes":["admin"],"org_id":"other","team_id":"other"}`, lead, csrf); w.Code != http.StatusOK {
		t.Fatalf("lead mint = %d %s", w.Code, w.Body)
	}
	if m := fa.lastMint; m.TeamId != "team-1" || m.OrgId != "org-1" || len(m.Scopes) != 1 || m.Scopes[0] != "generate" {
		t.Fatalf("minted %+v; want team-1/org-1 from the team record, generate only", m)
	}
}

// A membership entry without a team is refused at parse time, so a lead with
// no team cannot match every untenanted key.
func TestInvalidMembershipEntriesAreSkipped(t *testing.T) {
	// No team, a role that would grant global powers, a typo'd role.
	if m := auth.ParseTeamMemberships("x@corp.com::teamlead x2@corp.com:t1:admin x3@corp.com:t1:team-lead"); len(m) != 0 {
		t.Fatalf("invalid entries parsed: %v", m)
	}
}

func TestExplicitMembershipBeatsAdminPattern(t *testing.T) {
	h, _ := tenantServer(t)
	// dev@corp.com matches the *@corp.com admin pattern but is listed as a member.
	if w := call(h, "PUT", "/api/proxy/models/m2", `{"vendor":"openai","enabled":true}`, sessionCookie(t, "dev@corp.com"), csrf); w.Code != http.StatusForbidden {
		t.Fatalf("member matched by an admin pattern could write: %d", w.Code)
	}
}

func TestCabinetShowsEnforcedSpend(t *testing.T) {
	h, _ := tenantServer(t)
	w := call(h, "GET", "/api/proxy/teams/team-1/cabinet", "", sessionCookie(t, "dev@corp.com"), nil)
	var cab struct {
		Metered   bool    `json:"metered"`
		Spend     float64 `json:"current_spend_usd"`
		Remaining float64 `json:"remaining_usd"`
		Team      struct {
			Budget float64 `json:"monthly_budget_usd"`
		} `json:"team"`
		Keys []struct {
			CreatedAt string `json:"created_at"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cab); err != nil || !cab.Metered || cab.Spend != 40 || cab.Remaining != 60 {
		t.Fatalf("cabinet = %s (%v), want the gateway counter: $40 spent, $60 left", w.Body, err)
	}
	// Same encoding as every other endpoint: proto field names, RFC 3339 times.
	if cab.Team.Budget != 100 || len(cab.Keys) != 1 || cab.Keys[0].CreatedAt != "2026-10-01T00:00:00Z" {
		t.Fatalf("cabinet = %s, want protojson team and keys", w.Body)
	}
	if w := call(h, "GET", "/api/proxy/teams/team-2/cabinet", "", sessionCookie(t, "dev@corp.com"), nil); w.Code != http.StatusForbidden {
		t.Fatalf("another team's cabinet = %d, want 403", w.Code)
	}
	w = call(h, "GET", "/api/proxy/teams/team-1/spend", "", sessionCookie(t, "dev@corp.com"), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"limit_usd":100`) || strings.Contains(w.Body.String(), "keys") {
		t.Fatalf("team spend = %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", "/api/proxy/teams/team-2/spend", "", sessionCookie(t, "dev@corp.com"), nil); w.Code != http.StatusForbidden {
		t.Fatalf("another team's spend = %d, want 403", w.Code)
	}
}

func TestParseTeamMembershipsSplitsOnSpaces(t *testing.T) {
	m := auth.ParseTeamMemberships("a@corp.com:t1 b@corp.com:t2:teamlead")
	if m["a@corp.com"].TeamID != "t1" || m["b@corp.com"].Role != auth.RoleTeamLead {
		t.Fatalf("parsed %v", m)
	}
}

// Usage reads every capped team's counter in one request; it is global data.
func TestTeamsSpendBatch(t *testing.T) {
	h, _ := tenantServer(t)
	w := call(h, "GET", "/api/proxy/team-spend", "", sessionCookie(t, "p@view.com"), nil)
	var res struct {
		Teams []struct {
			ID    string  `json:"team_id"`
			Spent float64 `json:"current_spend_usd"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || len(res.Teams) != 1 || res.Teams[0].ID != "team-1" || res.Teams[0].Spent != 40 {
		t.Fatalf("team-spend = %d %s (%v)", w.Code, w.Body, err)
	}
	if w := call(h, "GET", "/api/proxy/team-spend", "", sessionCookie(t, "lead@corp.com"), nil); w.Code != http.StatusForbidden {
		t.Fatalf("team lead team-spend = %d, want 403", w.Code)
	}
}

func TestCountersDownIsOnlyAnOutage(t *testing.T) {
	if !countersDown(status.Error(codes.Unavailable, "redis")) || countersDown(status.Error(codes.InvalidArgument, "bad id")) || countersDown(status.Error(codes.PermissionDenied, "key")) {
		t.Fatal("only Unavailable and DeadlineExceeded may read as counters down")
	}
}
