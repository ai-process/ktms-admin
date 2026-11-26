package api

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/ai-process/ktms/gen/ktms/v1"
)

type explainAdmin struct {
	*fakeAdmin
	got *pb.ExplainRouteRequest
}

func (f *explainAdmin) ExplainRoute(_ context.Context, in *pb.ExplainRouteRequest, _ ...grpc.CallOption) (*pb.ExplainRouteResponse, error) {
	f.got = in
	return &pb.ExplainRouteResponse{Selected: true, Needs: in.GetNeeds(), Models: []string{"m1"},
		Candidates: []*pb.RouteCandidate{{ModelId: "m1", Excess: 1, BlendedPricePerMtok: 0.5}}}, nil
}

func TestExplainPassesNeeds(t *testing.T) {
	s, fa, _ := newServer(t, "")
	ea := &explainAdmin{fakeAdmin: fa}
	s.admin = ea
	w := call(s.Handler(), "POST", "/api/proxy/explain", `{"api_key_name":"billing","needs":{"medical":"basic"}}`, sessionCookie(t, "boss@x.com"), csrf)
	if w.Code != 200 {
		t.Fatalf("explain = %d %s", w.Code, w.Body)
	}
	if ea.got.GetApiKeyName() != "billing" || ea.got.GetNeeds()["medical"] != "basic" {
		t.Fatalf("request = %v", ea.got)
	}
	w = call(s.Handler(), "POST", "/api/proxy/explain", `{"needs":{}}`, sessionCookie(t, "boss@x.com"), csrf)
	if w.Code != 400 {
		t.Errorf("without a key = %d", w.Code)
	}
}

func TestPlaygroundNeeds(t *testing.T) {
	s, _, _ := newServer(t, "")
	fd := s.data.(*fakeData)
	w := call(s.Handler(), "POST", "/api/proxy/playground", `{"prompt":"hi","model":"auto","effort":"","needs":{"medical":"basic"}}`, sessionCookie(t, "boss@x.com"), csrf)
	if w.Code != 200 {
		t.Fatalf("playground = %d %s", w.Code, w.Body)
	}
	if fd.lastGenerateReq.GetNeeds()["medical"] != "basic" || fd.lastGenerateReq.GetEffort() != pb.Effort_EFFORT_UNSPECIFIED {
		t.Fatalf("needs %v, effort %v: needs without effort must not add a medium default", fd.lastGenerateReq.GetNeeds(), fd.lastGenerateReq.GetEffort())
	}
}

// A save from a form without the field keeps the model's competencies.
func TestUpsertKeepsCompetencies(t *testing.T) {
	s, fa, _ := newServer(t, "")
	fa.config = &pb.GetConfigResponse{Models: []*pb.ModelSpec{{Id: "m1", Vendor: "openai", Competencies: map[string]string{"general": "expert"}}}}
	w := call(s.Handler(), "PUT", "/api/proxy/models/m1", `{"vendor":"openai","efforts":["EFFORT_LOW"]}`, sessionCookie(t, "boss@x.com"), csrf)
	if w.Code != 200 {
		t.Fatalf("upsert = %d %s", w.Code, w.Body)
	}
	if fa.upserted.GetCompetencies()["general"] != "expert" {
		t.Fatalf("competencies lost: %v", fa.upserted)
	}
}
