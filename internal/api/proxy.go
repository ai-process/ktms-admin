package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/ai-process/ktms-admin/internal/auth"
	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
	"github.com/ai-process/ktms-admin/internal/usage"
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

func (s *Server) proxyListKeys(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyMintKey(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var req pb.MintAPIKeyRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	resp, err := s.admin.MintAPIKey(ctx, &req)
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, resp)
}

func (s *Server) proxyRevokeKey(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.RevokeAPIKey(ctx, &pb.RevokeAPIKeyRequest{Name: r.PathValue("name")}); err != nil {
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

func (s *Server) proxyUpsertModel(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxyDeleteModel(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteModel(ctx, &pb.DeleteModelRequest{Id: r.PathValue("id")}); err != nil {
		backendErr(w, err)
		return
	}
	s.dropPrices()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyReplaceRules(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxySetUserRateLimit(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxyDeleteUserRateLimit(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxySetVendorKey(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxyDeleteVendorKey(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.DeleteVendorKey(ctx, &pb.DeleteVendorKeyRequest{ApiKeyName: q.Get("api_key_name"), Vendor: q.Get("vendor")}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) proxyRotate(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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
		writeErr(w, http.StatusNotFound, "not_found", "KTMS data client not available")
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
		writeErr(w, http.StatusNotFound, "not_found", "KTMS data client not available")
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
		writeErr(w, http.StatusNotFound, "not_found", "KTMS data client not available")
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

func (s *Server) proxyDrainNode(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxyMintNodeKey(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

func (s *Server) proxyRevokeNodeKey(w http.ResponseWriter, r *http.Request, _ *auth.User) {
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

// modelPrices returns cached prices, or nil when KTMS is not configured
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
