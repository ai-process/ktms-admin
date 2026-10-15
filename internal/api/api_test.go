package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
	"github.com/ai-process/llm-proxy-admin/internal/config"
	pb "github.com/ai-process/llm-proxy-admin/internal/gen/llmproxy/v1"
	"github.com/ai-process/llm-proxy-admin/internal/usage"
)

type fakeAdmin struct {
	pb.LLMProxyAdminServiceClient
	upserted *pb.ModelSpec
	rules    *pb.ReplaceRulesRequest
	config   *pb.GetConfigResponse
}

type fakeData struct {
	pb.LLMProxyServiceClient
	lastGenerateReq *pb.GenerateTextRequest
	lastJudgeReq    *pb.JudgeRequest
	streamChunks    []*pb.GenerateTextChunk
	streamErr       error
}

func (f *fakeData) GenerateText(_ context.Context, in *pb.GenerateTextRequest, _ ...grpc.CallOption) (*pb.GenerateTextResponse, error) {
	f.lastGenerateReq = in
	return &pb.GenerateTextResponse{
		Choices:        []string{"echo: " + in.Messages[0].Text},
		ResolvedModel:  "m1",
		ResolvedVendor: "openai",
		Usage:          &pb.TokensUsage{Input: 15, Output: 25},
		Details:        []*pb.Choice{{Index: 0, Text: "echo: " + in.Messages[0].Text, ToolCalls: in.Messages[0].ToolCalls}},
	}, nil
}

func (f *fakeData) GenerateTextStream(_ context.Context, in *pb.GenerateTextStreamRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.GenerateTextChunk], error) {
	f.lastGenerateReq = in.Request
	return &fakeStream{chunks: f.streamChunks, err: f.streamErr}, nil
}

func (f *fakeData) Judge(_ context.Context, in *pb.JudgeRequest, _ ...grpc.CallOption) (*pb.JudgeResponse, error) {
	f.lastJudgeReq = in
	answers := make(map[string]*pb.JudgeAnswer, len(in.Questions))
	for id, q := range in.Questions {
		switch q.Type {
		case pb.JudgeType_JUDGE_TYPE_NOUL:
			answers[id] = &pb.JudgeAnswer{Type: q.Type, Noul: 0.85, Confidence: 0.9}
		case pb.JudgeType_JUDGE_TYPE_CHOICE:
			answers[id] = &pb.JudgeAnswer{Type: q.Type, Choice: "opt1", Probabilities: map[string]float64{"opt1": 0.8, "opt2": 0.2}, Confidence: 0.85}
		case pb.JudgeType_JUDGE_TYPE_SCORE:
			answers[id] = &pb.JudgeAnswer{Type: q.Type, Score: 3.5, Probabilities: map[string]float64{"0": 0.1, "1": 0.9}, Confidence: 0.9}
		}
	}
	return &pb.JudgeResponse{
		Answers:        answers,
		ResolvedModel:  "m-judge",
		ResolvedVendor: "anthropic",
		Usage:          &pb.TokensUsage{Input: 40, Output: 10},
	}, nil
}

type fakeStream struct {
	grpc.ServerStreamingClient[pb.GenerateTextChunk]
	chunks []*pb.GenerateTextChunk
	err    error
	idx    int
}

func (s *fakeStream) Recv() (*pb.GenerateTextChunk, error) {
	if s.idx < len(s.chunks) {
		c := s.chunks[s.idx]
		s.idx++
		return c, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return nil, io.EOF
}

func (f *fakeAdmin) GetConfig(context.Context, *pb.GetConfigRequest, ...grpc.CallOption) (*pb.GetConfigResponse, error) {
	if f.config != nil {
		return f.config, nil
	}
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
	fd := &fakeData{}
	s.admin = fa
	s.data = fd
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

func TestUpsertModelPreservesOperationalFields(t *testing.T) {
	s, fa, _ := newServer(t, "")
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	fa.config = &pb.GetConfigResponse{
		Models: []*pb.ModelSpec{
			{
				Id:                "m_existing",
				Vendor:            "openai",
				Rpm:               100,
				RpmBurst:          15,
				QueueTimeoutMs:    250,
				BudgetGraceMargin: 0.1,
				Enabled:           true,
			},
		},
	}

	body := `{"vendor":"openai","rpm":120,"enabled":true}`
	if w := call(h, "PUT", "/api/proxy/models/m_existing", body, boss, csrf); w.Code != 200 {
		t.Fatalf("upsert model = %d, body = %s", w.Code, w.Body)
	}
	if fa.upserted == nil {
		t.Fatal("expected model to reach proxy")
	}
	if fa.upserted.Rpm != 120 {
		t.Errorf("expected rpm 120, got %d", fa.upserted.Rpm)
	}
	if fa.upserted.RpmBurst != 15 {
		t.Errorf("expected rpm_burst 15 preserved, got %d", fa.upserted.RpmBurst)
	}
	if fa.upserted.QueueTimeoutMs != 250 {
		t.Errorf("expected queue_timeout_ms 250 preserved, got %d", fa.upserted.QueueTimeoutMs)
	}
	if fa.upserted.BudgetGraceMargin != 0.1 {
		t.Errorf("expected budget_grace_margin 0.1 preserved, got %f", fa.upserted.BudgetGraceMargin)
	}

	bodyZero := `{"vendor":"openai","rpm":120,"rpm_burst":0,"queue_timeout_ms":0,"budget_grace_margin":0,"enabled":true}`
	if w := call(h, "PUT", "/api/proxy/models/m_existing", bodyZero, boss, csrf); w.Code != 200 {
		t.Fatalf("upsert model zero = %d, body = %s", w.Code, w.Body)
	}
	if fa.upserted.RpmBurst != 0 {
		t.Errorf("expected rpm_burst 0, got %d", fa.upserted.RpmBurst)
	}
	if fa.upserted.QueueTimeoutMs != 0 {
		t.Errorf("expected queue_timeout_ms 0, got %d", fa.upserted.QueueTimeoutMs)
	}
	if fa.upserted.BudgetGraceMargin != 0 {
		t.Errorf("expected budget_grace_margin 0, got %f", fa.upserted.BudgetGraceMargin)
	}
}

func TestReplaceRulesPreservesWeights(t *testing.T) {
	s, fa, _ := newServer(t, "")
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	fa.config = &pb.GetConfigResponse{
		Rules: []*pb.RuleSpec{
			{
				Name:     "rule_weighted",
				Priority: 10,
				Effort:   pb.Effort_EFFORT_LOW,
				Use:      []string{"m1", "m2"},
				Weights:  []int32{80, 20},
				Enabled:  true,
			},
		},
	}

	body := `{"rules":[{"name":"rule_weighted","priority":15,"effort":"EFFORT_LOW","use":["m1","m2"],"enabled":true}]}`
	if w := call(h, "PUT", "/api/proxy/rules", body, boss, csrf); w.Code != 200 {
		t.Fatalf("replace rules = %d, body = %s", w.Code, w.Body)
	}
	if len(fa.rules.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(fa.rules.Rules))
	}
	r := fa.rules.Rules[0]
	if r.Priority != 15 {
		t.Errorf("expected priority 15, got %d", r.Priority)
	}
	if len(r.Weights) != 2 || r.Weights[0] != 80 || r.Weights[1] != 20 {
		t.Errorf("expected weights [80, 20] preserved, got %v", r.Weights)
	}

	bodyExplicit := `{"rules":[{"name":"rule_weighted","priority":15,"effort":"EFFORT_LOW","use":["m1","m2"],"weights":[60,40],"enabled":true}]}`
	if w := call(h, "PUT", "/api/proxy/rules", bodyExplicit, boss, csrf); w.Code != 200 {
		t.Fatalf("replace rules explicit = %d, body = %s", w.Code, w.Body)
	}
	r = fa.rules.Rules[0]
	if len(r.Weights) != 2 || r.Weights[0] != 60 || r.Weights[1] != 40 {
		t.Errorf("expected weights [60, 40], got %v", r.Weights)
	}

	bodyChainChange := `{"rules":[{"name":"rule_weighted","priority":15,"effort":"EFFORT_LOW","use":["m1","m2","m3"],"enabled":true}]}`
	if w := call(h, "PUT", "/api/proxy/rules", bodyChainChange, boss, csrf); w.Code != 200 {
		t.Fatalf("replace rules chain change = %d, body = %s", w.Code, w.Body)
	}
	r = fa.rules.Rules[0]
	if len(r.Weights) != 0 {
		t.Errorf("expected empty weights on chain change, got %v", r.Weights)
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

func TestPlaygroundUnary(t *testing.T) {
	s, _, _ := newServer(t, "")
	fd := s.data.(*fakeData)
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	// 1. Simple prompt
	body := `{"prompt":"hello world","model":"auto","effort":"high"}`
	w := call(h, "POST", "/api/proxy/playground", body, boss, csrf)
	if w.Code != 200 {
		t.Fatalf("playground unary = %d %s", w.Code, w.Body)
	}
	if fd.lastGenerateReq == nil || len(fd.lastGenerateReq.Messages) != 1 {
		t.Fatal("expected 1 message in GenerateTextRequest")
	}
	if fd.lastGenerateReq.Effort != pb.Effort_EFFORT_HIGH {
		t.Errorf("expected EFFORT_HIGH, got %v", fd.lastGenerateReq.Effort)
	}
	if fd.lastGenerateReq.Messages[0].Text != "hello world" {
		t.Errorf("expected text 'hello world', got %s", fd.lastGenerateReq.Messages[0].Text)
	}

	// 2. Multi-turn conversation with tool calls
	bodyMulti := `{
		"model": "m1",
		"messages": [
			{"role": "user", "text": "What is the weather in Paris?"},
			{"role": "assistant", "text": "", "tool_calls": [{"id": "call_123", "type": "function", "function": {"name": "get_weather", "arguments": "{\"location\":\"Paris\"}"}}]},
			{"role": "tool", "tool_call_id": "call_123", "text": "{\"temp\": 20}"}
		],
		"tools": [
			{"type": "function", "function": {"name": "get_weather", "description": "Get current weather"}}
		]
	}`
	w = call(h, "POST", "/api/proxy/playground", bodyMulti, boss, csrf)
	if w.Code != 200 {
		t.Fatalf("playground multi-turn = %d %s", w.Code, w.Body)
	}
	if len(fd.lastGenerateReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(fd.lastGenerateReq.Messages))
	}
	if fd.lastGenerateReq.Messages[1].Role != pb.MessageRole_MESSAGE_ROLE_ASSISTANT {
		t.Errorf("expected assistant role, got %v", fd.lastGenerateReq.Messages[1].Role)
	}
	if len(fd.lastGenerateReq.Messages[1].ToolCalls) != 1 || fd.lastGenerateReq.Messages[1].ToolCalls[0].Id != "call_123" {
		t.Errorf("expected tool_call_id call_123, got %v", fd.lastGenerateReq.Messages[1].ToolCalls)
	}
	if fd.lastGenerateReq.Messages[2].Role != pb.MessageRole_MESSAGE_ROLE_TOOL || fd.lastGenerateReq.Messages[2].ToolCallId != "call_123" {
		t.Errorf("expected tool role and id, got %v", fd.lastGenerateReq.Messages[2])
	}
	if len(fd.lastGenerateReq.Tools) != 1 || fd.lastGenerateReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("expected tool get_weather, got %v", fd.lastGenerateReq.Tools)
	}
}

func TestPlaygroundStream(t *testing.T) {
	s, _, _ := newServer(t, "")
	fd := s.data.(*fakeData)
	fd.streamChunks = []*pb.GenerateTextChunk{
		{Delta: "Hello", ResolvedModel: "m1", ResolvedVendor: "openai"},
		{Delta: " world!", FinishReason: "stop", Usage: &pb.TokensUsage{Input: 5, Output: 2}},
	}
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	body := `{"prompt":"stream test","model":"m1"}`
	w := call(h, "POST", "/api/proxy/playground/stream", body, boss, csrf)
	if w.Code != 200 {
		t.Fatalf("stream status = %d %s", w.Code, w.Body)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream content type, got %s", ct)
	}

	raw := w.Body.String()
	if !strings.Contains(raw, "event: chunk") {
		t.Errorf("expected chunk events in SSE output, got: %s", raw)
	}
	if !strings.Contains(raw, "Hello") || !strings.Contains(raw, " world!") {
		t.Errorf("expected chunk deltas in SSE output, got: %s", raw)
	}
	if !strings.Contains(raw, "event: done") {
		t.Errorf("expected done event in SSE output, got: %s", raw)
	}
}

func TestPlaygroundStreamError(t *testing.T) {
	s, _, _ := newServer(t, "")
	fd := s.data.(*fakeData)
	fd.streamErr = status.Error(codes.ResourceExhausted, "throttled:rpm")
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	body := `{"prompt":"error test"}`
	w := call(h, "POST", "/api/proxy/playground/stream", body, boss, csrf)
	if w.Code != 200 {
		t.Fatalf("stream initial status = %d", w.Code)
	}
	raw := w.Body.String()
	if !strings.Contains(raw, "event: error") || !strings.Contains(raw, "throttled:rpm") {
		t.Errorf("expected error event with message in SSE output, got: %s", raw)
	}
}

func TestJudge(t *testing.T) {
	s, _, _ := newServer(t, "")
	fd := s.data.(*fakeData)
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	body := `{
		"state_json": {"dialogue": [{"role": "user", "text": "refund please"}, {"role": "agent", "text": "Sure"}]},
		"model_override": "jev-latest",
		"questions": {
			"q_noul": {
				"type": "NOUL",
				"instructions": "Did the agent agree to refund?",
				"true_criteria": "Agent agreed",
				"false_criteria": "Agent refused"
			},
			"q_choice": {
				"type": "CHOICE",
				"instructions": "Tone of response",
				"options": {"polite": "Helpful and polite", "rude": "Impolite"}
			},
			"q_score": {
				"type": "SCORE",
				"instructions": "Response quality",
				"levels": ["poor", "fair", "good", "excellent"]
			}
		}
	}`
	w := call(h, "POST", "/api/proxy/judge", body, boss, csrf)
	if w.Code != 200 {
		t.Fatalf("judge status = %d %s", w.Code, w.Body)
	}
	if fd.lastJudgeReq == nil {
		t.Fatal("expected JudgeRequest to reach proxy")
	}
	if fd.lastJudgeReq.ModelOverride != "jev-latest" {
		t.Errorf("expected model override jev-latest, got %s", fd.lastJudgeReq.ModelOverride)
	}
	if len(fd.lastJudgeReq.Questions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(fd.lastJudgeReq.Questions))
	}
	if fd.lastJudgeReq.Questions["q_noul"].Type != pb.JudgeType_JUDGE_TYPE_NOUL {
		t.Errorf("expected NOUL type, got %v", fd.lastJudgeReq.Questions["q_noul"].Type)
	}
	if fd.lastJudgeReq.Questions["q_choice"].Type != pb.JudgeType_JUDGE_TYPE_CHOICE {
		t.Errorf("expected CHOICE type, got %v", fd.lastJudgeReq.Questions["q_choice"].Type)
	}
	if fd.lastJudgeReq.Questions["q_score"].Type != pb.JudgeType_JUDGE_TYPE_SCORE {
		t.Errorf("expected SCORE type, got %v", fd.lastJudgeReq.Questions["q_score"].Type)
	}

	respStr := w.Body.String()
	if !strings.Contains(respStr, "answers") || !strings.Contains(respStr, "0.85") {
		t.Errorf("expected judge answer with calibrated probability in response: %s", respStr)
	}
}

func TestJudgeValidation(t *testing.T) {
	s, _, _ := newServer(t, "")
	h := s.Handler()
	boss := sessionCookie(t, "boss@x.com")

	// Missing state
	w := call(h, "POST", "/api/proxy/judge", `{"questions":{"q":{"type":"NOUL","instructions":"test"}}}`, boss, csrf)
	if w.Code != 400 {
		t.Errorf("expected 400 on missing state, got %d", w.Code)
	}

	// Missing questions
	w = call(h, "POST", "/api/proxy/judge", `{"state_text":"some state","questions":{}}`, boss, csrf)
	if w.Code != 400 {
		t.Errorf("expected 400 on empty questions, got %d", w.Code)
	}

	// Unknown question type
	w = call(h, "POST", "/api/proxy/judge", `{"state_text":"some state","questions":{"q":{"type":"UNKNOWN","instructions":"test"}}}`, boss, csrf)
	if w.Code != 400 {
		t.Errorf("expected 400 on unknown question type, got %d", w.Code)
	}
}
