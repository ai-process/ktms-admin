package api

import (
	"encoding/base64"
	"strings"
	"testing"

	pb "github.com/ai-process/ktms/gen/ktms/v1"
)

// Images reach the Judge RPC named, a box question keeps its type, and a
// request of several MiB passes the console's body limit.
func TestJudgeImagesAndBox(t *testing.T) {
	s, _, _ := newServer(t, "")
	fd := s.data.(*fakeData)
	big := base64.StdEncoding.EncodeToString(make([]byte, 3<<20))
	body := `{"images":[{"name":"road","mime_type":"image/jpeg","data":"` + big + `"}],
		"questions":{"crack":{"type":"BOX","instructions":"the crack in road"}}}`
	w := call(s.Handler(), "POST", "/api/proxy/judge", body, sessionCookie(t, "boss@x.com"), csrf)
	if w.Code != 200 {
		t.Fatalf("judge = %d %s", w.Code, strings.TrimSpace(w.Body.String())[:min(200, w.Body.Len())])
	}
	req := fd.lastJudgeReq
	if len(req.GetStateMedia()) != 1 || req.GetStateMedia()[0].GetName() != "road" || len(req.GetStateMedia()[0].GetImage().GetData()) != 3<<20 {
		t.Fatalf("state media = %d parts", len(req.GetStateMedia()))
	}
	if req.GetQuestions()["crack"].GetType() != pb.JudgeType_JUDGE_TYPE_BOX || req.GetStateJson() != "" {
		t.Fatalf("request = type %v state %q", req.GetQuestions()["crack"].GetType(), req.GetStateJson())
	}
}
