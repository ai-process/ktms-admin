package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
	pb "github.com/ai-process/llm-proxy-admin/internal/gen/llmproxy/v1"
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

func (s *Server) proxyUpsertModel(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var m pb.ModelSpec
	if err := readProto(r, &m); err != nil {
		backendErr(w, badBody(err))
		return
	}
	m.Id = r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
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
	var req pb.ReplaceRulesRequest
	if err := readProto(r, &req); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if _, err := s.admin.ReplaceRules(ctx, &req); err != nil {
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

type playgroundReq struct {
	Model      string            `json:"model"` // "", "auto", or an exact model id
	Effort     string            `json:"effort"`
	System     string            `json:"system"`
	Prompt     string            `json:"prompt"`
	Attributes map[string]string `json:"attributes"`
	MaxTokens  int32             `json:"max_tokens"`
}

// proxyPlayground sends one prompt through the data plane using the console's
// admin key, which the proxy accepts for every scope.
func (s *Server) proxyPlayground(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req playgroundReq
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "a prompt is required")
		return
	}
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	resp, err := s.data.GenerateText(ctx, &pb.GenerateTextRequest{
		Effort: effort, Attributes: attrs, ActionId: "console.playground", ModelOverride: override,
		BaseSystemInstruction: req.System, MaxOutputTokens: req.MaxTokens,
		Messages: []*pb.ChatMessage{{Role: pb.MessageRole_MESSAGE_ROLE_USER, Text: req.Prompt}},
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
