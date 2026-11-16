package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ai-process/ktms-admin/internal/auth"
	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
)

// agentsServer is KTMS's built-in MCP server of the agents' inbox; a key
// allowed only it can talk to agents and do nothing else.
const agentsServer = "agents"

// asConsole runs one call with the admin key, which no read key can replace:
// minting, revoking and reading messages for a person's own agent only, with
// the owner checked before each.
func (s *Server) asConsole(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyCtx{}, s.cfg.ProxyAdminKey)
}

// owner is the person as KTMS stores a key's owner.
func owner(u *auth.User) string { return strings.ToLower(strings.TrimSpace(u.Email)) }

type agentPage struct {
	Email  string           `json:"email"`
	MCPURL string           `json:"mcp_url,omitempty"`
	Keys   []map[string]any `json:"keys"`
	Agents []map[string]any `json:"agents"`
}

// agentKey reports whether a key is the person's agent key, as this page
// mints them; the owner is checked here too, not left to KTMS's filter.
func agentKey(k *pb.APIKeyInfo, who string) bool {
	return !k.GetDisabled() && k.GetOwner() == who && slices.Equal(k.GetScopes(), []string{"mcp"}) &&
		slices.Equal(k.GetPolicy().GetAllowedMcp(), []string{agentsServer})
}

func (s *Server) agentGet(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	who := owner(u)
	var keys *pb.ListAPIKeysResponse
	var kerr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		keys, kerr = s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{Owner: who})
	}()
	// Revoked agents too: their conversations stay readable until they expire.
	agents, err := s.admin.ListAgents(ctx, &pb.ListAgentsRequest{Owner: who, IncludeRevoked: true})
	<-done
	if err == nil {
		err = kerr
	}
	if err != nil {
		backendErr(w, err)
		return
	}
	page := agentPage{Email: who, MCPURL: s.cfg.AgentsMCPURL, Keys: []map[string]any{}, Agents: []map[string]any{}}
	for _, k := range keys.GetKeys() {
		if !agentKey(k, who) {
			continue
		}
		v, err := protoValue(k)
		if err != nil {
			backendErr(w, err)
			return
		}
		page.Keys = append(page.Keys, v)
	}
	for _, a := range agents.GetAgents() {
		if a.GetOwner() != who {
			continue
		}
		v, err := protoValue(a)
		if err != nil {
			backendErr(w, err)
			return
		}
		page.Agents = append(page.Agents, v)
	}
	writeJSON(w, http.StatusOK, page)
}

var notLocal, notLabel = regexp.MustCompile(`[^a-z0-9._-]+`), regexp.MustCompile(`[^a-z0-9]+`)

// agentName is the key's, and so the agent's, name, by which agents address
// each other: the address with "_" for "@" (no domain has one, so two
// addresses never meet) and a label. A long one keeps a hash of the address.
func agentName(email, label string) (string, error) {
	addr := strings.ToLower(strings.TrimSpace(email))
	rawLocal, rawDomain, _ := strings.Cut(addr, "@")
	local := strings.Trim(notLocal.ReplaceAllString(rawLocal, "-"), "-._")
	domain := strings.Trim(notLocal.ReplaceAllString(rawDomain, "-"), "-._")
	if local == "" || domain == "" {
		return "", fmt.Errorf("cannot make an agent name of %q", email)
	}
	name := local + "_" + domain
	// An address that lost characters, or a long one, keeps a hash of the
	// whole, so two people never share a name. Only ASCII is left to cut.
	if local != rawLocal || domain != rawDomain || len(name) > 40 {
		sum := sha256.Sum256([]byte(addr))
		name = strings.TrimRight(name[:min(len(name), 32)], "-._") + "-" + hex.EncodeToString(sum[:])[:7]
	}
	l := strings.Trim(notLabel.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if len(l) > 20 {
		return "", fmt.Errorf("the label is longer than 20 characters")
	}
	if l != "" {
		name += "-" + l
	}
	return name, nil
}

func (s *Server) agentMintKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil {
		backendErr(w, badBody(err))
		return
	}
	base, err := agentName(u.Email, in.Label)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(s.asConsole(r.Context()), rpcTimeout)
	defer cancel()
	// No team: a team lead could edit a team key's policy, and a team's own
	// allowed_mcp might leave out agents. accept_from names people by owner:.
	req := &pb.MintAPIKeyRequest{
		Scopes: []string{"mcp"}, Owner: owner(u),
		Policy: &pb.APIKeyPolicy{AllowedMcp: []string{agentsServer}},
	}
	// A revoked key keeps its name, so a second agent with the same label is name-2.
	for i := 1; i <= 9; i++ {
		req.Name = base
		if i > 1 {
			req.Name = fmt.Sprintf("%s-%d", base, i)
		}
		resp, err := s.admin.MintAPIKey(ctx, req)
		if status.Code(err) == codes.AlreadyExists {
			continue
		}
		if err != nil {
			backendErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": req.Name, "plain_key": resp.GetPlainKey()})
		return
	}
	writeErr(w, http.StatusConflict, "conflict", "the names "+base+" to "+base+"-9 are taken; use another label")
}

func (s *Server) agentRevokeKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	keys, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{Owner: owner(u)})
	if err != nil {
		backendErr(w, err)
		return
	}
	if !slices.ContainsFunc(keys.GetKeys(), func(k *pb.APIKeyInfo) bool { return k.GetName() == name && agentKey(k, owner(u)) }) {
		writeErr(w, http.StatusNotFound, "not_found", "you have no agent key "+name)
		return
	}
	if _, err := s.admin.RevokeAPIKey(s.asConsole(ctx), &pb.RevokeAPIKeyRequest{Name: name}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ownAgents is the names of the person's agents.
func (s *Server) ownAgents(ctx context.Context, u *auth.User) ([]string, error) {
	res, err := s.admin.ListAgents(ctx, &pb.ListAgentsRequest{Owner: owner(u), IncludeRevoked: true})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, a := range res.GetAgents() {
		if a.GetOwner() == owner(u) {
			names = append(names, a.GetName())
		}
	}
	return names, nil
}

func (s *Server) agentTasks(w http.ResponseWriter, r *http.Request, u *auth.User) {
	agent := r.URL.Query().Get("agent")
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	own, err := s.ownAgents(ctx, u)
	if err != nil {
		backendErr(w, err)
		return
	}
	if !slices.Contains(own, agent) {
		writeErr(w, http.StatusNotFound, "not_found", "you have no agent "+agent)
		return
	}
	res, err := s.admin.ListAgentTasks(ctx, &pb.ListAgentTasksRequest{Agent: agent, Limit: 100})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, res)
}

func (s *Server) agentTask(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	own, err := s.ownAgents(ctx, u)
	if err != nil {
		backendErr(w, err)
		return
	}
	t, err := s.admin.GetAgentTask(s.asConsole(ctx), &pb.GetAgentTaskRequest{TaskId: r.PathValue("id")})
	if err != nil && status.Code(err) != codes.NotFound && status.Code(err) != codes.InvalidArgument {
		backendErr(w, err)
		return
	}
	// Someone else's task answers exactly as a missing one.
	if err != nil || !slices.Contains(own, t.GetFrom()) && !slices.Contains(own, t.GetTo()) {
		writeErr(w, http.StatusNotFound, "not_found", "no such task")
		return
	}
	writeProto(w, t)
}

// agentReply answers a task, or corrects an ended one's answer, as the person
// who owns one of its agents. KTMS checks the owner too; the console checks
// first so someone else's task answers exactly as a missing one.
func (s *Server) agentReply(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Agent      string `json:"agent"`
		Text       string `json:"text"`
		Status     string `json:"status"`
		Correction bool   `json:"correction"`
	}
	if err := readJSON(r, &in); err != nil {
		backendErr(w, badBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	own, err := s.ownAgents(ctx, u)
	if err != nil {
		backendErr(w, err)
		return
	}
	id := r.PathValue("id")
	t, err := s.admin.GetAgentTask(s.asConsole(ctx), &pb.GetAgentTaskRequest{TaskId: id})
	if err != nil && status.Code(err) != codes.NotFound && status.Code(err) != codes.InvalidArgument {
		backendErr(w, err)
		return
	}
	if err != nil || !slices.Contains(own, in.Agent) || in.Agent != t.GetFrom() && in.Agent != t.GetTo() {
		writeErr(w, http.StatusNotFound, "not_found", "no such task")
		return
	}
	out, err := s.admin.ReplyAgentTask(s.asConsole(ctx), &pb.ReplyAgentTaskRequest{
		Agent: in.Agent, Owner: owner(u), TaskId: id, Text: in.Text, Status: in.Status, Correction: in.Correction,
	})
	if err != nil {
		backendErr(w, err)
		return
	}
	writeProto(w, out)
}
