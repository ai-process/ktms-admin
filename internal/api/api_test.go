package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
	"github.com/ai-process/llm-proxy-admin/internal/config"
	pb "github.com/ai-process/llm-proxy-admin/internal/gen/llmproxy/v1"
	"github.com/ai-process/llm-proxy-admin/internal/usage"
)

type fakeAdmin struct {
	pb.LLMProxyAdminServiceClient
	upserted *pb.ModelSpec
	rules    *pb.ReplaceRulesRequest
}

func (f *fakeAdmin) GetConfig(context.Context, *pb.GetConfigRequest, ...grpc.CallOption) (*pb.GetConfigResponse, error) {
	return &pb.GetConfigResponse{Models: []*pb.ModelSpec{{Id: "m1", Vendor: "openai", PriceInPerMtok: 1, PriceOutPerMtok: 2, Enabled: true}}}, nil
}
func (f *fakeAdmin) UpsertModel(_ context.Context, r *pb.UpsertModelRequest, _ ...grpc.CallOption) (*pb.UpsertModelResponse, error) {
	f.upserted = r.Model
	return &pb.UpsertModelResponse{}, nil
}
func (f *fakeAdmin) ReplaceRules(_ context.Context, r *pb.ReplaceRulesRequest, _ ...grpc.CallOption) (*pb.ReplaceRulesResponse, error) {
	f.rules = r
	return &pb.ReplaceRulesResponse{}, nil
}
func (f *fakeAdmin) ListAPIKeys(context.Context, *pb.ListAPIKeysRequest, ...grpc.CallOption) (*pb.ListAPIKeysResponse, error) {
	return &pb.ListAPIKeysResponse{Keys: []*pb.APIKeyInfo{{Name: "k", Scopes: []string{"generate"}}}}, nil
}

func newServer(t *testing.T, tokenmeterURL string) (*Server, *fakeAdmin, *auth.Handler) {
	t.Helper()
	admins, _ := auth.ParseAllowlist("boss@x.com")
	viewers, _ := auth.ParseAllowlist("*@view.com")
	signer := auth.NewSigner("0123456789abcdef0123456789abcdef")
	h := auth.New(auth.Options{PublicURL: "https://admin.example.com", Admins: admins, Viewers: viewers, Signer: signer, SessionTTL: time.Hour})
	cfg := &config.Config{PublicURL: "https://admin.example.com"}
	s := &Server{cfg: cfg, auth: h}
	fa := &fakeAdmin{}
	s.admin = fa
	s.priceFetch = s.fetchPrices
	if tokenmeterURL != "" {
		s.usage = usage.NewClient(tokenmeterURL)
	}
	return s, fa, h
}

func sessionCookie(t *testing.T, email string) *http.Cookie {
	t.Helper()
	// Sealed with the same signer settings as newServer; payload keys mirror sessionData.
	tok, err := auth.NewSigner("0123456789abcdef0123456789abcdef").Seal(map[string]any{"e": email, "n": "N", "x": time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "lpa_session", Value: tok}
}

func call(h http.Handler, method, path, body string, c *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if c != nil {
		r.AddCookie(c)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var csrf = map[string]string{"X-Requested-With": "llm-proxy-admin", "Content-Type": "application/json"}

func TestAPIRequiresSession(t *testing.T) {
	s, _, _ := newServer(t, "")
	h := s.Handler()
	if w := call(h, "GET", "/api/proxy/config", "", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous = %d", w.Code)
	}
	if w := call(h, "GET", "/api/proxy/config", "", sessionCookie(t, "stranger@y.com"), nil); w.Code != 401 {
		t.Fatalf("stranger = %d", w.Code)
	}
	if w := call(h, "GET", "/api/proxy/config", "", sessionCookie(t, "boss@x.com"), nil); w.Code != 200 {
		t.Fatalf("admin = %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", "/", "", nil, nil); w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "/auth/login") {
		t.Fatalf("index should redirect to login: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestMutationsNeedRoleAndCSRF(t *testing.T) {
	s, fa, _ := newServer(t, "")
	h := s.Handler()
	body := `{"vendor":"openai","efforts":["EFFORT_LOW"],"enabled":true}`
	boss, viewer := sessionCookie(t, "boss@x.com"), sessionCookie(t, "p@view.com")

	if w := call(h, "PUT", "/api/proxy/models/m2", body, boss, nil); w.Code != 403 {
		t.Errorf("no csrf header = %d", w.Code)
	}
	cross := map[string]string{"X-Requested-With": "llm-proxy-admin", "Origin": "https://evil.example"}
	if w := call(h, "PUT", "/api/proxy/models/m2", body, boss, cross); w.Code != 403 {
		t.Errorf("cross origin = %d", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/models/m2", body, viewer, csrf); w.Code != 403 {
		t.Errorf("viewer write = %d", w.Code)
	}
	if fa.upserted != nil {
		t.Fatal("rejected request reached the proxy")
	}
	if w := call(h, "PUT", "/api/proxy/models/m2", body, boss, csrf); w.Code != 200 || fa.upserted.Id != "m2" || fa.upserted.Vendor != "openai" {
		t.Errorf("admin write = %d %v", w.Code, fa.upserted)
	}
	if w := call(h, "GET", "/api/proxy/keys", "", viewer, nil); w.Code != 200 {
		t.Errorf("viewer read = %d", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/rules", `{"rules":[{"name":"d","priority":1,"use":["m1"],"enabled":true}]}`, boss, csrf); w.Code != 200 || len(fa.rules.Rules) != 1 {
		t.Errorf("rules = %d", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/rules", `{"rules":[{"bogus":1}]}`, boss, csrf); w.Code != 400 {
		t.Errorf("bad body = %d", w.Code)
	}
}

func TestUpsertModelPreservesBatchPrices(t *testing.T) {
	s, fa, _ := newServer(t, "")
	h := s.Handler()
	body := `{"vendor":"ollama","efforts":["EFFORT_LOW"],"price_in_batch_per_mtok":0.5,"price_out_batch_per_mtok":1.5,"enabled":true}`
	boss := sessionCookie(t, "boss@x.com")

	if w := call(h, "PUT", "/api/proxy/models/m_ollama", body, boss, csrf); w.Code != 200 {
		t.Fatalf("upsert model = %d, body = %s", w.Code, w.Body)
	}
	if fa.upserted == nil {
		t.Fatal("expected model to reach proxy")
	}
	if fa.upserted.Vendor != "ollama" {
		t.Errorf("expected vendor ollama, got %s", fa.upserted.Vendor)
	}
	if fa.upserted.PriceInBatchPerMtok != 0.5 {
		t.Errorf("expected price_in_batch_per_mtok 0.5, got %f", fa.upserted.PriceInBatchPerMtok)
	}
	if fa.upserted.PriceOutBatchPerMtok != 1.5 {
		t.Errorf("expected price_out_batch_per_mtok 1.5, got %f", fa.upserted.PriceOutBatchPerMtok)
	}
}

func TestUsageReportUsesPrices(t *testing.T) {
	var gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"rows":[{"bucket":"2026-10-01T00:00:00Z","project_id":"p","model":"m1","status":"success","tokens_input":1000000,"tokens_output":1000000,"tokens_total":2000000,"request_count":4,"sum_duration_ms":400}]}`))
	}))
	defer up.Close()
	s, _, _ := newServer(t, up.URL)
	h := s.Handler()
	w := call(h, "GET", "/api/usage?from=2026-10-01&to=2026-10-02&group=project", "", sessionCookie(t, "p@view.com"), nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, want := range []string{"group_by_project_id=true", "group_by_model=true", "group_by_status=true", "from=2026-10-01T00%3A00%3A00Z", "to=2026-10-03T00%3A00%3A00Z"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("upstream query %q missing %q", gotQuery, want)
		}
	}
	if !strings.Contains(w.Body.String(), `"cost_usd":3`) { // 1M in x $1 + 1M out x $2
		t.Errorf("cost missing: %s", w.Body)
	}
	if w := call(h, "GET", "/api/usage?group=bogus", "", sessionCookie(t, "p@view.com"), nil); w.Code != 400 {
		t.Errorf("bad group = %d", w.Code)
	}
	if w := call(h, "GET", "/api/usage?from=2026-01-01&to=2020-01-01", "", sessionCookie(t, "p@view.com"), nil); w.Code != 400 {
		t.Errorf("inverted range = %d", w.Code)
	}
}

func TestMissingBackendRoutesAre404(t *testing.T) {
	s, _, _ := newServer(t, "")
	s.admin = nil
	w := call(s.Handler(), "GET", "/api/proxy/config", "", sessionCookie(t, "boss@x.com"), nil)
	if w.Code != 404 {
		t.Errorf("disabled backend = %d", w.Code)
	}
}
