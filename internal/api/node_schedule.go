package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ai-process/ktms-admin/internal/auth"
	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
)

// Node schedules are what KTMS sends nodes that hand their schedule to it
// (KTMS 1.5+); a node on its own schedule ignores them.

func (s *Server) proxyGetNodeSchedule(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.GetNodeSchedule(ctx, &pb.GetNodeScheduleRequest{NodeId: r.PathValue("id")})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxySetNodeSchedule(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.SetNodeSchedule(ctx, &pb.SetNodeScheduleRequest{NodeId: r.PathValue("id"), Text: req.Text})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyDeleteNodeSchedule(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteNodeSchedule(ctx, &pb.DeleteNodeScheduleRequest{NodeId: r.PathValue("id")}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// proxySetNodeOverride holds a node at a level for minutes from now; the
// console sends a duration so the browser's clock does not matter.
func (s *Server) proxySetNodeOverride(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var req struct {
		Level   *uint32 `json:"level"`
		Minutes int     `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	if req.Level == nil || req.Minutes <= 0 {
		backendErr(w, badBody(errors.New("level and minutes are required")))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.SetNodeCapacityOverride(ctx, &pb.SetNodeCapacityOverrideRequest{
		NodeId: r.PathValue("id"), Level: *req.Level, Until: timestamppb.New(time.Now().Add(time.Duration(req.Minutes) * time.Minute)),
	})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyClearNodeOverride(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.SetNodeCapacityOverride(ctx, &pb.SetNodeCapacityOverrideRequest{NodeId: r.PathValue("id"), Clear: true})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}
