package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
)

func (f *fakeAdmin) GetNodeSchedule(_ context.Context, r *pb.GetNodeScheduleRequest, _ ...grpc.CallOption) (*pb.NodeScheduleInfo, error) {
	return &pb.NodeScheduleInfo{NodeId: r.NodeId, Text: "mon-fri 09:00-18:00 0\n", Version: 2}, nil
}

func (f *fakeAdmin) SetNodeSchedule(_ context.Context, r *pb.SetNodeScheduleRequest, _ ...grpc.CallOption) (*pb.NodeScheduleInfo, error) {
	if strings.Contains(r.Text, "bad") {
		return nil, status.Error(codes.InvalidArgument, "schedule: line 1: bad time")
	}
	f.lastSchedule = r
	return &pb.NodeScheduleInfo{NodeId: r.NodeId, Text: r.Text, Version: 3}, nil
}

func (f *fakeAdmin) SetNodeCapacityOverride(_ context.Context, r *pb.SetNodeCapacityOverrideRequest, _ ...grpc.CallOption) (*pb.NodeScheduleInfo, error) {
	f.lastOverride = r
	return &pb.NodeScheduleInfo{NodeId: r.NodeId, Version: 4}, nil
}

func (f *fakeAdmin) DeleteNodeSchedule(context.Context, *pb.DeleteNodeScheduleRequest, ...grpc.CallOption) (*pb.DeleteNodeScheduleResponse, error) {
	return &pb.DeleteNodeScheduleResponse{}, nil
}

func TestNodeScheduleRoutes(t *testing.T) {
	s, fa, _ := newServer(t, "")
	h := s.Handler()
	boss, viewer := sessionCookie(t, "boss@x.com"), sessionCookie(t, "p@view.com")

	if w := call(h, "GET", "/api/proxy/nodes/mac/schedule", "", viewer, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":"2"`) {
		t.Fatalf("viewer read = %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/api/proxy/nodes/mac/schedule", `{"text":"x"}`, viewer, csrf); w.Code != 403 {
		t.Fatalf("viewer write = %d", w.Code)
	}
	if w := call(h, "PUT", "/api/proxy/nodes/mac/schedule", `{"text":"10:00-11:00 0\\n"}`, boss, csrf); w.Code != 200 || fa.lastSchedule.GetNodeId() != "mac" {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/api/proxy/nodes/mac/schedule", `{"text":"bad"}`, boss, csrf); w.Code != 400 || !strings.Contains(w.Body.String(), "line 1") {
		t.Fatalf("invalid rules = %d %s", w.Code, w.Body)
	}
	before := time.Now()
	if w := call(h, "PUT", "/api/proxy/nodes/mac/override", `{"level":0,"minutes":90}`, boss, csrf); w.Code != 200 {
		t.Fatalf("override = %d %s", w.Code, w.Body)
	}
	if u := fa.lastOverride.GetUntil().AsTime(); u.Sub(before) < 89*time.Minute || u.Sub(before) > 91*time.Minute || fa.lastOverride.GetLevel() != 0 {
		t.Fatalf("override sent = %v", fa.lastOverride)
	}
	if w := call(h, "PUT", "/api/proxy/nodes/mac/override", `{"minutes":90}`, boss, csrf); w.Code != 400 {
		t.Fatalf("override without level = %d", w.Code)
	}
	if w := call(h, "DELETE", "/api/proxy/nodes/mac/override", "", boss, csrf); w.Code != 200 || !fa.lastOverride.GetClear() {
		t.Fatalf("clear = %d", w.Code)
	}
	if w := call(h, "DELETE", "/api/proxy/nodes/mac/schedule", "", boss, csrf); w.Code != 200 {
		t.Fatalf("delete = %d", w.Code)
	}
}
