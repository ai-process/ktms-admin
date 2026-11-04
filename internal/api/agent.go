package api

import (
	"context"
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

// The routes under /api/agent serve every signed-in person, for their own
// agent only: the key is minted with them as its owner, and every read and
// revoke checks that owner. They run with the admin key, which a viewer's
// requests otherwise never use, because no read key may mint.
func (s *Server) asConsole(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyCtx{}, s.cfg.ProxyAdminKey)
}

type agentPage struct {
	Email  string           `json:"email"`
	MCPURL string           `json:"mcp_url,omitempty"`
	Keys   []map[string]any `json:"keys"`
	Agents []map[string]any `json:"agents"`
}

// agentKey reports whether a key is a person's agent key, as this page mints them.
func agentKey(k *pb.APIKeyInfo) bool {
	return !k.GetDisabled() && slices.Equal(k.GetScopes(), []string{"mcp"}) && slices.Equal(k.GetPolicy().GetAllowedMcp(), []string{agentsServer})
}

func (s *Server) agentGet(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx, cancel := context.WithTimeout(s.asConsole(r.Context()), rpcTimeout)
	defer cancel()
	owner := strings.ToLower(u.Email)
	keys, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{Owner: owner})
	if err != nil {
		backendErr(w, err)
		return
	}
	agents, err := s.admin.ListAgents(ctx, &pb.ListAgentsRequest{Owner: owner})
	if err != nil {
		backendErr(w, err)
		return
	}
	page := agentPage{Email: owner, MCPURL: s.cfg.AgentsMCPURL, Keys: []map[string]any{}, Agents: []map[string]any{}}
	for _, k := range keys.GetKeys() {
		if !agentKey(k) {
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
		v, err := protoValue(a)
		if err != nil {
			backendErr(w, err)
			return
		}
		page.Agents = append(page.Agents, v)
	}
	writeJSON(w, http.StatusOK, page)
}

var notSlug, startsSlug = regexp.MustCompile(`[^a-z0-9]+`), regexp.MustCompile(`^[a-z0-9]`)

func slug(s string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// agentName is the key's, and so the agent's, name: the person's address and
// a label, since agents address each other by it.
func agentName(email, label string) (string, error) {
	local, domain, _ := strings.Cut(strings.ToLower(email), "@")
	first, _, _ := strings.Cut(domain, ".")
	name := slug(local) + "." + slug(first)
	if l := slug(label); l != "" {
		name += "-" + l
	}
	if len(name) > 56 || !startsSlug.MatchString(name) {
		return "", fmt.Errorf("cannot make an agent name of %q and %q; use a shorter label", email, label)
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
	req := &pb.MintAPIKeyRequest{
		Scopes: []string{"mcp"}, Owner: strings.ToLower(u.Email),
		Policy: &pb.APIKeyPolicy{AllowedMcp: []string{agentsServer}},
	}
	if u.InTeam() {
		// A team member's agent belongs to the team, so accept_from can name it.
		t, err := s.admin.GetTeam(ctx, &pb.GetTeamRequest{Id: u.TeamID})
		if err != nil {
			backendErr(w, err)
			return
		}
		req.TeamId, req.OrgId = t.GetTeam().GetId(), t.GetTeam().GetOrgId()
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
	ctx, cancel := context.WithTimeout(s.asConsole(r.Context()), rpcTimeout)
	defer cancel()
	keys, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{Owner: strings.ToLower(u.Email)})
	if err != nil {
		backendErr(w, err)
		return
	}
	if !slices.ContainsFunc(keys.GetKeys(), func(k *pb.APIKeyInfo) bool { return k.GetName() == name && agentKey(k) }) {
		writeErr(w, http.StatusNotFound, "not_found", "you have no agent key "+name)
		return
	}
	if _, err := s.admin.RevokeAPIKey(ctx, &pb.RevokeAPIKeyRequest{Name: name}); err != nil {
		backendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ownAgents is the names of the person's agents.
func (s *Server) ownAgents(ctx context.Context, u *auth.User) ([]string, error) {
	res, err := s.admin.ListAgents(ctx, &pb.ListAgentsRequest{Owner: strings.ToLower(u.Email)})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, a := range res.GetAgents() {
		names = append(names, a.GetName())
	}
	return names, nil
}

func (s *Server) agentTasks(w http.ResponseWriter, r *http.Request, u *auth.User) {
	agent := r.URL.Query().Get("agent")
	ctx, cancel := context.WithTimeout(s.asConsole(r.Context()), rpcTimeout)
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
	ctx, cancel := context.WithTimeout(s.asConsole(r.Context()), rpcTimeout)
	defer cancel()
	own, err := s.ownAgents(ctx, u)
	if err != nil {
		backendErr(w, err)
		return
	}
	t, err := s.admin.GetAgentTask(ctx, &pb.GetAgentTaskRequest{TaskId: r.PathValue("id")})
	if err != nil && status.Code(err) != codes.NotFound {
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
