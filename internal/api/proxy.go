package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
	pb "github.com/ai-process/llm-proxy-admin/internal/gen/ktms/v1"
	"github.com/ai-process/llm-proxy-admin/internal/usage"
)

const rpcTimeout = 20 * time.Second

func (s *Server) proxyConfig(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.GetConfig(ctx, &pb.GetConfigRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyListKeys(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	// A team user sees their own team's keys only; the guard ensures they have one.
	if !u.CanReadGlobal() {
		resp.Keys = keysOfTeam(resp.Keys, u.TeamID)
	}
	writeProto(w, resp)
}

// keysOfTeam filters keys to one team. teamID is never empty here: an empty
// one would match every untenanted key, admin keys included.
func keysOfTeam(keys []*pb.APIKeyInfo, teamID string) []*pb.APIKeyInfo {
	var out []*pb.APIKeyInfo
	if teamID == "" {
		return out
	}
	for _, k := range keys {
		if k.TeamId == teamID {
			out = append(out, k)
		}
	}
	return out
}

func (s *Server) proxyMintKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req pb.MintAPIKeyRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if !u.IsSuperAdmin() {
		// A team lead mints generate keys for their own team; the org comes from
		// the team record, not the membership entry, so the binding is consistent.
		teamResp, err := s.admin.GetTeam(ctx, &pb.GetTeamRequest{Id: u.TeamID})
		if err != nil {
			backendErr(w, err)
			return
		}
		req.TeamId = teamResp.GetTeam().GetId()
		req.OrgId = teamResp.GetTeam().GetOrgId()
		req.Scopes = []string{"generate"}
	}
	resp, err := s.admin.MintAPIKey(ctx, &req)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyRevokeKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if !u.IsSuperAdmin() {
		keysResp, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{})
		if err != nil {
			backendErr(w, err)
			return
		}
		isOwnTeam := false
		for _, k := range keysOfTeam(keysResp.Keys, u.TeamID) {
			isOwnTeam = isOwnTeam || k.Name == name
		}
		if !isOwnTeam {
			writeErr(w, http.StatusForbidden, "forbidden", "cannot revoke keys belonging to other teams")
			return
		}
	}
	if _, err := s.admin.RevokeAPIKey(ctx, &pb.RevokeAPIKeyRequest{Name: name}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func hasField(m map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func (s *Server) proxyUpsertModel(w http.ResponseWriter, r *http.Request, u *auth.User) {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		backendErr(w, badBody(err))
		return
	}
	var m pb.ModelSpec
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, &m); err != nil {
		backendErr(w, badBody(err))
		return
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		backendErr(w, badBody(err))
		return
	}
	id := r.PathValue("id")
	m.Id = id

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	// Preserves omitted fields so partial updates or older clients don't zero them.
	if cfg, err := s.admin.GetConfig(ctx, &pb.GetConfigRequest{}); err == nil && cfg != nil {
		for _, em := range cfg.Models {
			if em.GetId() == id {
				if !hasField(rawMap, "rpm_burst", "rpmBurst") {
					m.RpmBurst = em.GetRpmBurst()
				}
				if !hasField(rawMap, "queue_timeout_ms", "queueTimeoutMs") {
					m.QueueTimeoutMs = em.GetQueueTimeoutMs()
				}
				if !hasField(rawMap, "budget_grace_margin", "budgetGraceMargin") {
					m.BudgetGraceMargin = em.GetBudgetGraceMargin()
				}
				break
			}
		}
	}

	if _, err := s.admin.UpsertModel(ctx, &pb.UpsertModelRequest{Model: &m}); err != nil {
		backendErr(w, err)
		return
	}
	s.dropPrices()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyDeleteModel(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteModel(ctx, &pb.DeleteModelRequest{Id: r.PathValue("id")}); err != nil {
		backendErr(w, err)
		return
	}
	s.dropPrices()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyReplaceRules(w http.ResponseWriter, r *http.Request, u *auth.User) {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		backendErr(w, badBody(err))
		return
	}
	var req pb.ReplaceRulesRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	var rawReq struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(raw, &rawReq); err != nil {
		backendErr(w, badBody(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	// Preserves existing rule weights if omitted in the update and chain length matches.
	if cfg, err := s.admin.GetConfig(ctx, &pb.GetConfigRequest{}); err == nil && cfg != nil {
		existingByName := make(map[string]*pb.RuleSpec, len(cfg.Rules))
		for _, er := range cfg.Rules {
			existingByName[er.GetName()] = er
		}
		for i, rule := range req.Rules {
			hasW := i < len(rawReq.Rules) && hasField(rawReq.Rules[i], "weights")
			if !hasW {
				if er, ok := existingByName[rule.GetName()]; ok {
					if len(er.GetWeights()) == len(rule.GetUse()) {
						rule.Weights = er.GetWeights()
					}
				}
			}
		}
	}

	if _, err := s.admin.ReplaceRules(ctx, &req); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyListUserRateLimits(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	userID := r.URL.Query().Get("user_id")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListUserRateLimits(ctx, &pb.ListUserRateLimitsRequest{UserId: userID})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxySetUserRateLimit(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var spec pb.UserRateLimitSpec
	if err := readProto(r, &spec); err != nil {
		backendErr(w, badBody(err))
		return
	}
	spec.UserId = strings.TrimSpace(spec.UserId)
	spec.ModelId = strings.TrimSpace(spec.ModelId)
	if spec.UserId == "" || spec.ModelId == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "user_id and model_id are required")
		return
	}
	if spec.Rpm < 0 || spec.QueueTimeoutMs < 0 || spec.DailyTokens < 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "limits must be non-negative")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.SetUserRateLimit(ctx, &pb.SetUserRateLimitRequest{RateLimit: &spec}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyDeleteUserRateLimit(w http.ResponseWriter, r *http.Request, u *auth.User) {
	q := r.URL.Query()
	userID := strings.TrimSpace(q.Get("user_id"))
	modelID := strings.TrimSpace(q.Get("model_id"))
	if userID == "" || modelID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "user_id and model_id query params are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteUserRateLimit(ctx, &pb.DeleteUserRateLimitRequest{UserId: userID, ModelId: modelID}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyListSpendLimits(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListSpendLimits(ctx, &pb.ListSpendLimitsRequest{SubjectType: q.Get("subject_type"), SubjectId: q.Get("subject_id")})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

// checkSpendSubject keeps the console to key and user caps: a team's cap
// belongs to the team row (Create/UpdateTeam), or the two would drift apart.
func checkSpendSubject(subjectType, subjectID, period string) string {
	switch {
	case subjectType != "key" && subjectType != "user":
		return "subject_type must be key or user; team budgets are set on the team"
	case subjectID == "":
		return "subject_id is required (* for the default)"
	case period != "day" && period != "month":
		return "period must be day or month"
	}
	return ""
}

func (s *Server) proxySetSpendLimit(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var spec pb.SpendLimitSpec
	if err := readProto(r, &spec); err != nil {
		backendErr(w, badBody(err))
		return
	}
	spec.SubjectType, spec.SubjectId, spec.Period = strings.TrimSpace(spec.SubjectType), strings.TrimSpace(spec.SubjectId), strings.TrimSpace(spec.Period)
	if msg := checkSpendSubject(spec.SubjectType, spec.SubjectId, spec.Period); msg != "" {
		writeErr(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	if spec.LimitNanousd <= 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "the cap must be above 0")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.SetSpendLimit(ctx, &pb.SetSpendLimitRequest{SpendLimit: &spec}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyDeleteSpendLimit(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	q := r.URL.Query()
	// subject_id is matched exactly: a delete must name the row that exists.
	st, sid, period := q.Get("subject_type"), q.Get("subject_id"), q.Get("period")
	if msg := checkSpendSubject(st, sid, period); msg != "" {
		writeErr(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteSpendLimit(ctx, &pb.DeleteSpendLimitRequest{SubjectType: st, SubjectId: sid, Period: period}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxySetVendorKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req pb.SetVendorKeyRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.SetVendorKey(ctx, &req); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyDeleteVendorKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteVendorKey(ctx, &pb.DeleteVendorKeyRequest{ApiKeyName: q.Get("api_key_name"), Vendor: q.Get("vendor")}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyRotate(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	resp, err := s.admin.RotateEncryption(ctx, &pb.RotateEncryptionRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

type playgroundMessage struct {
	Role       string         `json:"role"` // "user", "assistant", "tool"
	Text       string         `json:"text"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []*pb.ToolCall `json:"tool_calls,omitempty"`
}

type playgroundReq struct {
	Model      string              `json:"model"` // "", "auto", or an exact model id
	Effort     string              `json:"effort"`
	System     string              `json:"system"`
	Prompt     string              `json:"prompt"`
	Messages   []playgroundMessage `json:"messages"`
	Tools      []*pb.Tool          `json:"tools"`
	Attributes map[string]string   `json:"attributes"`
	MaxTokens  int32               `json:"max_tokens"`
}

// buildPlaygroundReq normalizes the playground payload into a gRPC request,
// resolving user attribution and multi-turn messages or tool calls.
func (s *Server) buildPlaygroundReq(req playgroundReq, u *auth.User) (*pb.GenerateTextRequest, error) {
	effort := pb.Effort_EFFORT_MEDIUM
	switch strings.ToLower(req.Effort) {
	case "low":
		effort = pb.Effort_EFFORT_LOW
	case "high":
		effort = pb.Effort_EFFORT_HIGH
	}
	override := ""
	if req.Model != "" && req.Model != "auto" {
		override = req.Model
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 512
	}
	attrs := req.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	if attrs["user_id"] == "" {
		attrs["user_id"] = u.Email
	}

	var msgs []*pb.ChatMessage
	if len(req.Messages) > 0 {
		for _, m := range req.Messages {
			role := pb.MessageRole_MESSAGE_ROLE_USER
			switch strings.ToLower(m.Role) {
			case "assistant":
				role = pb.MessageRole_MESSAGE_ROLE_ASSISTANT
			case "tool":
				role = pb.MessageRole_MESSAGE_ROLE_TOOL
			}
			msgs = append(msgs, &pb.ChatMessage{
				Role:       role,
				Text:       m.Text,
				ToolCallId: m.ToolCallID,
				ToolCalls:  m.ToolCalls,
			})
		}
	} else if strings.TrimSpace(req.Prompt) != "" {
		msgs = []*pb.ChatMessage{{Role: pb.MessageRole_MESSAGE_ROLE_USER, Text: req.Prompt}}
	} else {
		return nil, errors.New("a prompt or message history is required")
	}

	return &pb.GenerateTextRequest{
		Effort:                effort,
		Attributes:            attrs,
		ActionId:              "console.playground",
		ModelOverride:         override,
		BaseSystemInstruction: req.System,
		MaxOutputTokens:       req.MaxTokens,
		Messages:              msgs,
		Tools:                 req.Tools,
	}, nil
}

// proxyPlayground sends one prompt or conversation through the data plane using the console's
// admin key, which the proxy accepts for every scope.
func (s *Server) proxyPlayground(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.data == nil {
		writeErr(w, http.StatusNotFound, "not_found", "the KTMS gateway data client is not available")
		return
	}
	var req playgroundReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	genReq, err := s.buildPlaygroundReq(req, u)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	resp, err := s.data.GenerateText(ctx, genReq)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

// proxyPlaygroundStream proxies SSE chunks from GenerateTextStream to the browser,
// keeping the connection alive and flushing increments as they arrive.
func (s *Server) proxyPlaygroundStream(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.data == nil {
		writeErr(w, http.StatusNotFound, "not_found", "the KTMS gateway data client is not available")
		return
	}
	var req playgroundReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	genReq, err := s.buildPlaygroundReq(req, u)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}

	// Disable write deadline for this response so long streams don't time out.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	stream, err := s.data.GenerateTextStream(ctx, &pb.GenerateTextStreamRequest{Request: genReq})
	if err != nil {
		st, _ := status.FromError(err)
		errData, _ := json.Marshal(map[string]any{"error": map[string]string{"code": strings.ToLower(st.Code().String()), "message": st.Message()}})
		_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
		return
	}

	marshaler := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
			flusher.Flush()
			return
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
				return
			}
			st, _ := status.FromError(err)
			errData, _ := json.Marshal(map[string]any{"error": map[string]string{"code": strings.ToLower(st.Code().String()), "message": st.Message()}})
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
			flusher.Flush()
			return
		}
		b, err := marshaler.Marshal(chunk)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", b)
		flusher.Flush()
	}
}

type judgeQuestionReq struct {
	Type          string            `json:"type"` // "NOUL" | "CHOICE" | "SCORE"
	Instructions  string            `json:"instructions"`
	Options       map[string]string `json:"options,omitempty"`
	Levels        []string          `json:"levels,omitempty"`
	TrueCriteria  string            `json:"true_criteria,omitempty"`
	FalseCriteria string            `json:"false_criteria,omitempty"`
}

type judgeReq struct {
	StateJSON     json.RawMessage             `json:"state_json"`
	StateText     string                      `json:"state_text,omitempty"`
	Questions     map[string]judgeQuestionReq `json:"questions"`
	ModelOverride string                      `json:"model_override,omitempty"`
	Attributes    map[string]string           `json:"attributes,omitempty"`
}

// proxyJudge evaluates typed criteria against an arbitrary JSON state via the Judge RPC.
func (s *Server) proxyJudge(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.data == nil {
		writeErr(w, http.StatusNotFound, "not_found", "the KTMS gateway data client is not available")
		return
	}
	var req judgeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	stateStr := req.StateText
	if len(req.StateJSON) > 0 {
		stateStr = string(req.StateJSON)
	}
	if strings.TrimSpace(stateStr) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "state_json or state_text is required")
		return
	}
	if !json.Valid([]byte(stateStr)) {
		b, _ := json.Marshal(stateStr)
		stateStr = string(b)
	}

	if len(req.Questions) == 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "at least one question is required")
		return
	}

	questions := make(map[string]*pb.JudgeQuestion, len(req.Questions))
	for id, q := range req.Questions {
		qType := pb.JudgeType_JUDGE_TYPE_UNSPECIFIED
		switch strings.ToUpper(q.Type) {
		case "NOUL", "JUDGE_TYPE_NOUL", "1":
			qType = pb.JudgeType_JUDGE_TYPE_NOUL
		case "CHOICE", "JUDGE_TYPE_CHOICE", "2":
			qType = pb.JudgeType_JUDGE_TYPE_CHOICE
		case "SCORE", "JUDGE_TYPE_SCORE", "3":
			qType = pb.JudgeType_JUDGE_TYPE_SCORE
		default:
			writeErr(w, http.StatusBadRequest, "bad_request", "unknown question type for "+id)
			return
		}
		if strings.TrimSpace(q.Instructions) == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "instructions required for "+id)
			return
		}
		questions[id] = &pb.JudgeQuestion{
			Type:          qType,
			Instructions:  q.Instructions,
			Options:       q.Options,
			Levels:        q.Levels,
			TrueCriteria:  q.TrueCriteria,
			FalseCriteria: q.FalseCriteria,
		}
	}

	attrs := req.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	if attrs["user_id"] == "" {
		attrs["user_id"] = u.Email
	}

	override := ""
	if req.ModelOverride != "" && req.ModelOverride != "auto" {
		override = req.ModelOverride
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	resp, err := s.data.Judge(ctx, &pb.JudgeRequest{
		StateJson:     stateStr,
		Questions:     questions,
		Attributes:    attrs,
		ActionId:      "console.judge",
		ModelOverride: override,
	})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyListNodes(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListNodes(ctx, &pb.ListNodesRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyDrainNode(w http.ResponseWriter, r *http.Request, u *auth.User) {
	nodeID := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DrainNode(ctx, &pb.DrainNodeRequest{NodeId: nodeID}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyListNodeKeys(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListNodeKeys(ctx, &pb.ListNodeKeysRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyMintNodeKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req pb.MintNodeKeyRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.MintNodeKey(ctx, &req)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyRevokeNodeKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.RevokeNodeKey(ctx, &pb.RevokeNodeKeyRequest{Id: req.ID}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- organizations & teams ----

func (s *Server) proxyListOrganizations(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListOrganizations(ctx, &pb.ListOrganizationsRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyCreateOrganization(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req pb.CreateOrganizationRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.CreateOrganization(ctx, &req)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyDeleteOrganization(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteOrganization(ctx, &pb.DeleteOrganizationRequest{Id: r.PathValue("id")}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyListTeams(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if !u.CanReadGlobal() {
		teamResp, err := s.admin.GetTeam(ctx, &pb.GetTeamRequest{Id: u.TeamID})
		if err != nil {
			backendErr(w, err)
			return
		}
		writeProto(w, &pb.ListTeamsResponse{Teams: []*pb.Team{teamResp.Team}})
		return
	}
	orgID := r.URL.Query().Get("org_id")
	resp, err := s.admin.ListTeams(ctx, &pb.ListTeamsRequest{OrgId: orgID})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyCreateTeam(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req pb.CreateTeamRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.CreateTeam(ctx, &req)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

// canAccessTeam lets global readers see any team and team users only their own.
func canAccessTeam(w http.ResponseWriter, u *auth.User, id string) bool {
	if u.CanReadGlobal() || u.TeamID == id {
		return true
	}
	writeErr(w, http.StatusForbidden, "forbidden", "access to other teams is restricted")
	return false
}

func (s *Server) proxyGetTeam(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id := r.PathValue("id")
	if !canAccessTeam(w, u, id) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.GetTeam(ctx, &pb.GetTeamRequest{Id: id})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyUpdateTeam(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req pb.UpdateTeamRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	req.Id = r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.UpdateTeam(ctx, &req)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyDeleteTeam(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteTeam(ctx, &pb.DeleteTeamRequest{Id: r.PathValue("id")}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyTeamCabinet(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id := r.PathValue("id")
	if !canAccessTeam(w, u, id) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	var (
		wg       sync.WaitGroup
		keysResp *pb.ListAPIKeysResponse
		keysErr  error
		spend    *pb.GetTeamSpendResponse
		spendErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); keysResp, keysErr = s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{}) }()
	go func() { defer wg.Done(); spend, spendErr = s.admin.GetTeamSpend(ctx, &pb.GetTeamSpendRequest{Id: id}) }()
	teamResp, err := s.admin.GetTeam(ctx, &pb.GetTeamRequest{Id: id})
	wg.Wait()
	if err == nil {
		err = keysErr
	}
	if err != nil {
		backendErr(w, err)
		return
	}
	team, err := protoValue(teamResp.Team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "cannot encode response")
		return
	}
	keys, err := protoValue(&pb.ListAPIKeysResponse{Keys: keysOfTeam(keysResp.Keys, id)})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "cannot encode response")
		return
	}
	out := map[string]any{"team": team, "keys": keys["keys"]}
	addSpend(out, id, spend, spendErr)
	writeJSON(w, http.StatusOK, out)
}

// countersDown is a failure to read the counters themselves; anything else
// (bad id, the console's key refused) is a real error the caller must see.
func countersDown(err error) bool {
	c := status.Code(err)
	return c == codes.Unavailable || c == codes.DeadlineExceeded
}

// maxSpendReads bounds the GetTeamSpend calls one Usage page load can cause.
const maxSpendReads = 8

// proxyTeamsSpend reads every capped team's counter in one browser request,
// at most maxSpendReads at a time against the gateway.
func (s *Server) proxyTeamsSpend(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	teams, err := s.admin.ListTeams(ctx, &pb.ListTeamsRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	var capped []*pb.Team
	for _, t := range teams.Teams {
		if t.MonthlyBudgetUsd > 0 {
			capped = append(capped, t)
		}
	}
	out := make([]map[string]any, len(capped))
	sem := make(chan struct{}, maxSpendReads)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		fatal error
	)
	for i, t := range capped {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			spend, err := s.admin.GetTeamSpend(ctx, &pb.GetTeamSpendRequest{Id: t.Id})
			// A team deleted meanwhile is one bad row; anything else but a
			// counter outage (the console's key refused) fails the whole reply.
			if err != nil && !countersDown(err) && status.Code(err) != codes.NotFound {
				mu.Lock()
				fatal = err
				mu.Unlock()
			}
			row := map[string]any{"team_id": t.Id, "name": t.Name, "monthly_budget_usd": t.MonthlyBudgetUsd}
			addSpend(row, t.Id, spend, err)
			out[i] = row
		}()
	}
	wg.Wait()
	if fatal != nil {
		backendErr(w, fatal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": out})
}

// addSpend reports the gateway's own counter, the one its team cap refuses
// calls on. When it cannot be read the reply says so instead of showing the
// whole budget as free.
func addSpend(out map[string]any, id string, spend *pb.GetTeamSpendResponse, err error) {
	switch {
	case err != nil:
		log.Warn().Err(err).Str("team", id).Msg("team spend unavailable")
		out["spend_error"] = "spend counters unavailable"
	case !spend.GetMetered():
		out["metered"] = false
	default:
		spent := float64(spend.GetSpentNanousd()) / 1e9
		out["metered"] = true
		out["current_spend_usd"] = spent
		out["resets_at"] = spend.GetResetsAt().AsTime()
		if limit := spend.GetLimitNanousd(); limit > 0 {
			out["limit_usd"] = float64(limit) / 1e9
			out["remaining_usd"] = max(0, float64(limit)/1e9-spent)
		}
	}
}

// ---- prices (for cost estimates in the usage view) ----

const priceTTL = 30 * time.Second

func (s *Server) dropPrices() {
	s.priceMu.Lock()
	s.pricesAt = time.Time{}
	s.priceMu.Unlock()
}

func (s *Server) fetchPrices(ctx context.Context) (map[string]usage.Price, error) {
	resp, err := s.admin.GetConfig(ctx, &pb.GetConfigRequest{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]usage.Price, len(resp.Models))
	for _, m := range resp.Models {
		if m.PriceInPerMtok > 0 || m.PriceOutPerMtok > 0 {
			out[m.Id] = usage.Price{In: m.PriceInPerMtok, Out: m.PriceOutPerMtok}
		}
	}
	return out, nil
}

// modelPrices returns cached prices, or nil when llm-proxy is not configured
// or unreachable; the usage view then simply omits cost.
func (s *Server) modelPrices(ctx context.Context) map[string]usage.Price {
	if s.priceFetch == nil {
		return nil
	}
	s.priceMu.Lock()
	defer s.priceMu.Unlock()
	if s.prices != nil && time.Since(s.pricesAt) < priceTTL {
		return s.prices
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := s.priceFetch(ctx)
	if err != nil {
		return s.prices // stale or nil: cost is a nicety, never an error
	}
	s.prices, s.pricesAt = p, time.Now()
	return p
}
