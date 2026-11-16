package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ai-process/ktms-admin/internal/auth"
	"github.com/ai-process/ktms-admin/internal/auth/oidc"
	"github.com/ai-process/ktms-admin/internal/config"
	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
)

type agentAdmin struct {
	pb.AdminServiceClient
	ignoreOwner bool // an older KTMS drops the owner filter
	keys        []*pb.APIKeyInfo
	agents      []*pb.AgentInfo
	tasks       map[string]*pb.AgentTask
	lastMint    *pb.MintAPIKeyRequest
	revoked     []string
	replies     []*pb.ReplyAgentTaskRequest
}

func (f *agentAdmin) ReplyAgentTask(_ context.Context, r *pb.ReplyAgentTaskRequest, _ ...grpc.CallOption) (*pb.AgentTask, error) {
	f.replies = append(f.replies, r)
	t := f.tasks[r.GetTaskId()]
	return &pb.AgentTask{TaskId: t.GetTaskId(), Status: "completed", AnsweredBy: "owner"}, nil
}

func (f *agentAdmin) ListAPIKeys(_ context.Context, r *pb.ListAPIKeysRequest, _ ...grpc.CallOption) (*pb.ListAPIKeysResponse, error) {
	var out []*pb.APIKeyInfo
	for _, k := range f.keys {
		if f.ignoreOwner || r.GetOwner() == "" || k.GetOwner() == r.GetOwner() {
			out = append(out, k)
		}
	}
	return &pb.ListAPIKeysResponse{Keys: out}, nil
}

func (f *agentAdmin) MintAPIKey(_ context.Context, r *pb.MintAPIKeyRequest, _ ...grpc.CallOption) (*pb.MintAPIKeyResponse, error) {
	if slices.ContainsFunc(f.keys, func(k *pb.APIKeyInfo) bool { return k.GetName() == r.GetName() }) {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	f.lastMint = r
	f.keys = append(f.keys, &pb.APIKeyInfo{Name: r.GetName(), Scopes: r.GetScopes(), Owner: r.GetOwner(), Policy: r.GetPolicy(), TeamId: r.GetTeamId()})
	return &pb.MintAPIKeyResponse{PlainKey: "ktms_" + r.GetName()}, nil
}

func (f *agentAdmin) RevokeAPIKey(_ context.Context, r *pb.RevokeAPIKeyRequest, _ ...grpc.CallOption) (*pb.RevokeAPIKeyResponse, error) {
	f.revoked = append(f.revoked, r.GetName())
	return &pb.RevokeAPIKeyResponse{}, nil
}

func (f *agentAdmin) GetTeam(_ context.Context, r *pb.GetTeamRequest, _ ...grpc.CallOption) (*pb.GetTeamResponse, error) {
	return &pb.GetTeamResponse{Team: &pb.Team{Id: r.GetId(), OrgId: "org-1"}}, nil
}

func (f *agentAdmin) ListAgents(_ context.Context, r *pb.ListAgentsRequest, _ ...grpc.CallOption) (*pb.ListAgentsResponse, error) {
	var out []*pb.AgentInfo
	for _, a := range f.agents {
		if f.ignoreOwner || r.GetOwner() == "" || a.GetOwner() == r.GetOwner() {
			out = append(out, a)
		}
	}
	return &pb.ListAgentsResponse{Agents: out}, nil
}

func (f *agentAdmin) ListAgentTasks(_ context.Context, r *pb.ListAgentTasksRequest, _ ...grpc.CallOption) (*pb.ListAgentTasksResponse, error) {
	var out []*pb.AgentTask
	for _, t := range f.tasks {
		if t.GetFrom() == r.GetAgent() || t.GetTo() == r.GetAgent() {
			out = append(out, t)
		}
	}
	return &pb.ListAgentTasksResponse{Tasks: out}, nil
}

func (f *agentAdmin) GetAgentTask(_ context.Context, r *pb.GetAgentTaskRequest, _ ...grpc.CallOption) (*pb.AgentTask, error) {
	if t := f.tasks[r.GetTaskId()]; t != nil {
		return t, nil
	}
	return nil, status.Error(codes.NotFound, "no task")
}

// agentServer signs in boss@x.com as admin, dev@corp.com as a team member and
// everyone else at corp.com as self.
func agentServer(t *testing.T) (http.Handler, *agentAdmin) {
	t.Helper()
	admins, _ := auth.ParseAllowlist("boss@x.com")
	self, _ := auth.ParseAllowlist("*@corp.com")
	members := auth.ParseTeamMemberships("dev@corp.com:team-1:member")
	h := auth.New(auth.Options{Methods: []auth.Method{oidc.New(oidc.Options{})}, PublicURL: "https://admin.example.com",
		Admins: admins, SelfService: self, TeamMemberships: members,
		Signer: auth.NewSigner("0123456789abcdef0123456789abcdef"), SessionTTL: time.Hour})
	fa := &agentAdmin{
		keys: []*pb.APIKeyInfo{{Name: "bob_corp.com-agent", Owner: "bob@corp.com", Scopes: []string{"mcp"}, Policy: &pb.APIKeyPolicy{AllowedMcp: []string{"agents"}}}},
		agents: []*pb.AgentInfo{
			{Name: "ann_corp.com-agent", Owner: "ann@corp.com"},
			{Name: "bob_corp.com-agent", Owner: "bob@corp.com"},
		},
		tasks: map[string]*pb.AgentTask{
			"t-ann": {TaskId: "t-ann", From: "ann_corp.com-agent", To: "bob_corp.com-agent", Status: "submitted", Messages: []*pb.AgentMessage{{Text: "hi bob"}}},
			"t-bob": {TaskId: "t-bob", From: "bob_corp.com-agent", To: "other", Status: "working"},
		},
	}
	s := &Server{cfg: &config.Config{PublicURL: "https://admin.example.com", AgentsMCPURL: "https://ktms.corp.com/mcp/agents"}, auth: h, admin: fa, data: &fakeData{}}
	s.priceFetch = s.fetchPrices
	return s.Handler(), fa
}

func TestSelfServiceSeesOnlyItsAgent(t *testing.T) {
	h, fa := agentServer(t)
	ann := sessionCookie(t, "Ann@corp.com")

	var me struct {
		User struct{ Role string } `json:"user"`
	}
	if w := call(h, "GET", "/api/me", "", ann, nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &me) != nil || me.User.Role != auth.RoleSelf {
		t.Fatalf("me = %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/proxy/keys", "/api/proxy/config", "/api/proxy/teams"} {
		if w := call(h, "GET", path, "", ann, nil); w.Code != http.StatusForbidden {
			t.Errorf("self GET %s = %d, want 403", path, w.Code)
		}
	}
	if w := call(h, "POST", "/api/proxy/keys", `{"name":"x"}`, ann, csrf); w.Code != http.StatusForbidden {
		t.Errorf("self minted a generate key: %d", w.Code)
	}

	// The key is the person's, may only reach agents, and takes the next free name.
	fa.keys = append(fa.keys, &pb.APIKeyInfo{Name: "ann_corp.com-laptop", Disabled: true})
	w := call(h, "POST", "/api/agent/keys", `{"label":"Laptop"}`, ann, csrf)
	var minted struct {
		Name     string `json:"name"`
		PlainKey string `json:"plain_key"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &minted) != nil || minted.Name != "ann_corp.com-laptop-2" || minted.PlainKey == "" {
		t.Fatalf("mint = %d %s", w.Code, w.Body)
	}
	m := fa.lastMint
	if m.GetOwner() != "ann@corp.com" || !slices.Equal(m.GetScopes(), []string{"mcp"}) || !slices.Equal(m.GetPolicy().GetAllowedMcp(), []string{"agents"}) || m.GetTeamId() != "" {
		t.Fatalf("mint request = %v", m)
	}
	if w := call(h, "POST", "/api/agent/keys", `{}`, ann, nil); w.Code != http.StatusForbidden {
		t.Fatalf("mint without the CSRF header = %d", w.Code)
	}

	var page agentPage
	if w := call(h, "GET", "/api/agent", "", ann, nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil ||
		len(page.Keys) != 1 || len(page.Agents) != 1 || page.MCPURL == "" {
		t.Fatalf("page = %d %s", w.Code, w.Body)
	}

	if w := call(h, "DELETE", "/api/agent/keys/bob_corp.com-agent", "", ann, csrf); w.Code != http.StatusNotFound || len(fa.revoked) != 0 {
		t.Fatalf("revoked another person's key: %d %v", w.Code, fa.revoked)
	}
	if w := call(h, "DELETE", "/api/agent/keys/ann_corp.com-laptop-2", "", ann, csrf); w.Code != 200 || !slices.Equal(fa.revoked, []string{"ann_corp.com-laptop-2"}) {
		t.Fatalf("revoke own = %d %v", w.Code, fa.revoked)
	}

	if w := call(h, "GET", "/api/agent/tasks?agent=bob_corp.com-agent", "", ann, nil); w.Code != http.StatusNotFound {
		t.Fatalf("read another person's tasks: %d", w.Code)
	}
	if w := call(h, "GET", "/api/agent/tasks?agent=ann_corp.com-agent", "", ann, nil); w.Code != 200 {
		t.Fatalf("own tasks = %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", "/api/agent/tasks/t-ann", "", ann, nil); w.Code != 200 {
		t.Fatalf("own task = %d", w.Code)
	}
	for _, id := range []string{"t-bob", "nope"} {
		if w := call(h, "GET", "/api/agent/tasks/"+id, "", ann, nil); w.Code != http.StatusNotFound {
			t.Fatalf("task %s = %d, want 404", id, w.Code)
		}
	}
}

// A named admin outranks the company pattern; a team member's agent stays theirs.
func TestSelfServiceRoles(t *testing.T) {
	h, fa := agentServer(t)
	if w := call(h, "GET", "/api/proxy/keys", "", sessionCookie(t, "boss@x.com"), nil); w.Code != 200 {
		t.Fatal("the admin lost access")
	}
	if w := call(h, "POST", "/api/agent/keys", `{}`, sessionCookie(t, "dev@corp.com"), csrf); w.Code != 200 {
		t.Fatalf("member mint = %d %s", w.Code, w.Body)
	}
	// Not bound to the team: a team lead must not reach its policy.
	if fa.lastMint.GetName() != "dev_corp.com" || fa.lastMint.GetTeamId() != "" {
		t.Fatalf("member's agent key = %v", fa.lastMint)
	}
	if w := call(h, "GET", "/api/agent", "", sessionCookie(t, "eve@else.com"), nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a stranger reached the agent page: %d", w.Code)
	}
}

func TestAgentName(t *testing.T) {
	for _, c := range []struct{ email, label, want string }{
		{"Bob@Corp.com", "", "bob_corp.com"},
		{"bob@corp.de", "Laptop 2", "bob_corp.de-laptop-2"},
		{"ann.test@corp.com", "", "ann.test_corp.com"},
		{"ann-test@corp.com", "", "ann-test_corp.com"},
	} {
		if got, err := agentName(c.email, c.label); err != nil || got != c.want {
			t.Errorf("agentName(%q, %q) = %q, %v; want %q", c.email, c.label, got, err, c.want)
		}
	}
	// Characters that had to go leave a hash, so a+b and a-b never meet.
	plus, _ := agentName("a+b@corp.com", "")
	dash, _ := agentName("a-b@corp.com", "")
	if plus == dash || dash != "a-b_corp.com" {
		t.Errorf("a+b -> %q, a-b -> %q", plus, dash)
	}
	if idn, err := agentName("anna@bücher.de", ""); err != nil || strings.ContainsFunc(idn, func(r rune) bool { return r > 127 }) {
		t.Errorf("IDN domain -> %q, %v", idn, err)
	}
	long, err := agentName("firstname.middlename.lastname-contractor@verylongcompanyname.com", "laptop")
	other, _ := agentName("firstname.middlename.lastname-contractor@verylongcompanyname.org", "laptop")
	if err != nil || len(long) > 63 || long == other {
		t.Errorf("long names %q, %q, %v", long, other, err)
	}
	if _, err := agentName("x@corp.com", "a-very-long-label-indeed"); err == nil {
		t.Error("a label over 20 characters passed")
	}
}

// A team lead may not touch a key of the team that is not a generate key.
func TestTeamLeadCannotEditAgentKey(t *testing.T) {
	h, fa := tenantServer(t)
	fa.keys = append(fa.keys, &pb.APIKeyInfo{Name: "dev-agent", TeamId: "team-1", Scopes: []string{"mcp"}, Policy: &pb.APIKeyPolicy{AllowedMcp: []string{"agents"}}})
	lead := sessionCookie(t, "lead@corp.com")
	if w := call(h, "PUT", "/api/proxy/keys/dev-agent/policy", `{}`, lead, csrf); w.Code != http.StatusForbidden {
		t.Fatalf("team lead cleared an mcp key's policy: %d", w.Code)
	}
	if w := call(h, "DELETE", "/api/proxy/keys/dev-agent", "", lead, csrf); w.Code != http.StatusForbidden {
		t.Fatalf("team lead revoked an mcp key: %d", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/keys/search-service/policy", `{}`, lead, csrf); w.Code != 200 {
		t.Fatalf("team lead lost its own generate key: %d %s", w.Code, w.Body)
	}
}

// Against a KTMS that ignores the owner filter, nobody reaches another's key.
func TestAgentOwnerCheckedInConsole(t *testing.T) {
	h, fa := agentServer(t)
	fa.ignoreOwner = true
	ann := sessionCookie(t, "ann@corp.com")
	if w := call(h, "DELETE", "/api/agent/keys/bob_corp.com-agent", "", ann, csrf); w.Code != http.StatusNotFound || len(fa.revoked) != 0 {
		t.Fatalf("revoked bob's key: %d %v", w.Code, fa.revoked)
	}
	var page agentPage
	if w := call(h, "GET", "/api/agent", "", ann, nil); json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Keys) != 0 || len(page.Agents) != 1 {
		t.Fatalf("page = %s", w.Body)
	}
	if w := call(h, "GET", "/api/agent/tasks?agent=bob_corp.com-agent", "", ann, nil); w.Code != http.StatusNotFound {
		t.Fatalf("read bob's tasks: %d", w.Code)
	}
}

// A viewer opens the page but changes nothing; a team lead still reads the
// spend of a team key it may not change.
func TestViewerAndTeamLeadLimits(t *testing.T) {
	h, fa := tenantServer(t)
	if w := call(h, "POST", "/api/agent/keys", `{}`, sessionCookie(t, "p@view.com"), csrf); w.Code != http.StatusForbidden {
		t.Fatalf("viewer minted an agent key: %d", w.Code)
	}
	fa.keys = append(fa.keys, &pb.APIKeyInfo{Name: "mixed", TeamId: "team-1", Scopes: []string{"generate", "mcp"}})
	lead := sessionCookie(t, "lead@corp.com")
	if w := call(h, "GET", "/api/proxy/spend?subject_type=key&subject_id=mixed", "", lead, nil); w.Code != 200 {
		t.Fatalf("team lead spend of its mixed key = %d %s", w.Code, w.Body)
	}
	if w := call(h, "DELETE", "/api/proxy/keys/mixed", "", lead, csrf); w.Code != http.StatusForbidden {
		t.Fatalf("team lead revoked a mixed key: %d", w.Code)
	}
}

// A person answers and corrects only for their own agent, on a task it is in.
func TestOwnerReplyInConsole(t *testing.T) {
	h, fa := agentServer(t)
	bob := sessionCookie(t, "bob@corp.com")
	ann := sessionCookie(t, "ann@corp.com")
	for _, c := range []struct {
		who  *http.Cookie
		id   string
		body string
	}{
		{ann, "t-ann", `{"agent":"bob_corp.com-agent","text":"x"}`}, // someone else's agent
		{bob, "t-bob", `{"agent":"ann_corp.com-agent","text":"x"}`}, // someone else's agent
		{bob, "nope", `{"agent":"bob_corp.com-agent","text":"x"}`},  // no such task
	} {
		if w := call(h, "POST", "/api/agent/tasks/"+c.id+"/reply", c.body, c.who, csrf); w.Code != http.StatusNotFound {
			t.Fatalf("reply %s %s = %d", c.id, c.body, w.Code)
		}
	}
	if len(fa.replies) != 0 {
		t.Fatalf("refused replies reached KTMS: %v", fa.replies)
	}
	w := call(h, "POST", "/api/agent/tasks/t-ann/reply", `{"agent":"bob_corp.com-agent","text":"use main","status":"working"}`, bob, csrf)
	if w.Code != 200 || len(fa.replies) != 1 || fa.replies[0].GetOwner() != "bob@corp.com" || fa.replies[0].GetStatus() != "working" {
		t.Fatalf("own reply = %d %s, %v", w.Code, w.Body, fa.replies)
	}
	if w := call(h, "POST", "/api/agent/tasks/t-ann/reply", `{"agent":"bob_corp.com-agent","text":"fix","correction":true}`, bob, csrf); w.Code != 200 || !fa.replies[1].GetCorrection() {
		t.Fatalf("own correction = %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/agent/tasks/t-ann/reply", `{"agent":"bob_corp.com-agent","text":"x"}`, bob, nil); w.Code != http.StatusForbidden {
		t.Fatalf("reply without the CSRF header = %d", w.Code)
	}
}
