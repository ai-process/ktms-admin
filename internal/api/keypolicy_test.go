package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
)

var spendMu sync.Mutex

func (f *fakeAdmin) SetAPIKeyPolicy(_ context.Context, r *pb.SetAPIKeyPolicyRequest, _ ...grpc.CallOption) (*pb.SetAPIKeyPolicyResponse, error) {
	f.lastPolicy = r
	return &pb.SetAPIKeyPolicyResponse{}, nil
}

func (f *fakeAdmin) GetSpend(_ context.Context, r *pb.GetSpendRequest, _ ...grpc.CallOption) (*pb.GetSpendResponse, error) {
	spendMu.Lock() // key-spend calls it concurrently
	f.lastSpend = r
	spendMu.Unlock()
	return &pb.GetSpendResponse{Windows: []*pb.SpendWindow{{Period: "day", SpentNanousd: 1e9, LimitNanousd: 5e9, RemainingNanousd: 4e9, Metered: true}}}, nil
}

func TestKeyPolicyAccess(t *testing.T) {
	h, fa := tenantServer(t)
	lead, dev, admin := sessionCookie(t, "lead@corp.com"), sessionCookie(t, "dev@corp.com"), sessionCookie(t, "boss@x.com")
	body := `{"allowedModels":["m1"],"rpmLimit":60,"tpmLimit":"1000"}`

	if w := call(h, "PUT", "/api/proxy/keys/billing-service/policy", body, lead, csrf); w.Code != http.StatusForbidden || fa.lastPolicy != nil {
		t.Fatalf("lead on another team's key = %d, want 403 and no change", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/keys/search-service/policy", body, dev, csrf); w.Code != http.StatusForbidden || fa.lastPolicy != nil {
		t.Fatalf("member = %d, want 403", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/keys/search-service/policy", body, lead, csrf); w.Code != http.StatusOK {
		t.Fatalf("lead on own key = %d %s", w.Code, w.Body)
	}
	if p := fa.lastPolicy; p.GetName() != "search-service" || p.GetPolicy().GetRpmLimit() != 60 || p.GetPolicy().GetTpmLimit() != 1000 || len(p.GetPolicy().GetAllowedModels()) != 1 {
		t.Fatalf("policy sent = %v", p)
	}
	if w := call(h, "PUT", "/api/proxy/keys/billing-service/policy", body, admin, csrf); w.Code != http.StatusOK {
		t.Fatalf("admin = %d %s", w.Code, w.Body)
	}
}

func TestSpendAccess(t *testing.T) {
	h, fa := tenantServer(t)
	dev, viewer := sessionCookie(t, "dev@corp.com"), sessionCookie(t, "p@view.com")
	for path, want := range map[string]int{
		"/api/proxy/spend?subject_type=key&subject_id=search-service":  http.StatusOK,
		"/api/proxy/spend?subject_type=team&subject_id=team-1":         http.StatusOK,
		"/api/proxy/spend?subject_type=key&subject_id=billing-service": http.StatusForbidden,
		"/api/proxy/spend?subject_type=user&subject_id=u1":             http.StatusForbidden,
		"/api/proxy/spend?subject_type=team&subject_id=team-2":         http.StatusForbidden,
	} {
		if w := call(h, "GET", path, "", dev, nil); w.Code != want {
			t.Errorf("member GET %s = %d, want %d", path, w.Code, want)
		}
	}
	w := call(h, "GET", "/api/proxy/spend?subject_type=user&subject_id=u1", "", viewer, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"remaining_nanousd":"4000000000"`) || fa.lastSpend.GetSubjectId() != "u1" {
		t.Fatalf("viewer = %d %s", w.Code, w.Body)
	}
}

func TestKeySpendIsScopedAndBounded(t *testing.T) {
	h, fa := tenantServer(t)
	fa.keys = append(fa.keys, &pb.APIKeyInfo{Name: "old-search", TeamId: "team-1", Disabled: true})
	var got struct {
		Keys map[string]struct {
			Windows []struct {
				Period string `json:"period"`
			} `json:"windows"`
		} `json:"keys"`
	}
	w := call(h, "GET", "/api/proxy/key-spend", "", sessionCookie(t, "lead@corp.com"), nil)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("lead = %d %s", w.Code, w.Body)
	}
	if _, ok := got.Keys["search-service"]; !ok || len(got.Keys) != 1 {
		t.Fatalf("lead sees %v, want only the team's active key", got.Keys)
	}
	if got.Keys["search-service"].Windows[0].Period != "day" {
		t.Fatalf("windows = %v", got.Keys["search-service"].Windows)
	}
	w = call(h, "GET", "/api/proxy/key-spend", "", sessionCookie(t, "boss@x.com"), nil)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Keys) != 3 {
		t.Fatalf("admin sees %v (%v), want the three active keys", got.Keys, err)
	}
}
