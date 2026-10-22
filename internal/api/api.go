// Package api serves the console's JSON API and static files, enforcing the
// session, role and CSRF rules in one place.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
	"github.com/ai-process/llm-proxy-admin/internal/config"
	pb "github.com/ai-process/llm-proxy-admin/internal/gen/ktms/v1"
	"github.com/ai-process/llm-proxy-admin/internal/usage"
	"github.com/ai-process/llm-proxy-admin/internal/web"
)

const csrfHeader = "X-Requested-With"
const csrfValue = "llm-proxy-admin"

type Server struct {
	cfg   *config.Config
	auth  *auth.Handler
	admin pb.AdminServiceClient
	data  pb.GatewayServiceClient
	usage *usage.Client

	priceMu    sync.Mutex
	prices     map[string]usage.Price
	pricesAt   time.Time
	priceFetch func(ctx context.Context) (map[string]usage.Price, error)
}

// New wires the backends named in cfg. Either may be absent.
func New(cfg *config.Config, a *auth.Handler) (*Server, func(), error) {
	s := &Server{cfg: cfg, auth: a}
	closer := func() {}
	if cfg.ProxyEnabled() {
		creds := credentials.NewClientTLSFromCert(nil, "")
		if !cfg.ProxyTLS {
			creds = nil
		}
		opts := []grpc.DialOption{
			grpc.WithUnaryInterceptor(attachKey(cfg.ProxyAdminKey)),
			grpc.WithStreamInterceptor(attachStreamKey(cfg.ProxyAdminKey)),
		}
		if creds == nil {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(creds))
		}
		conn, err := grpc.NewClient(cfg.ProxyAddr, opts...)
		if err != nil {
			return nil, nil, err
		}
		closer = func() { _ = conn.Close() }
		s.admin = pb.NewAdminServiceClient(conn)
		s.data = pb.NewGatewayServiceClient(conn)
		s.priceFetch = s.fetchPrices
	}
	if cfg.UsageEnabled() {
		s.usage = usage.NewClient(cfg.TokenmeterURL)
	}
	return s, closer, nil
}

func attachKey(key string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, inv grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key)
		return inv(ctx, method, req, reply, cc, opts...)
	}
}

func attachStreamKey(key string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.auth.Mount(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })

	api := func(pattern string, pol policy, h func(w http.ResponseWriter, r *http.Request, u *auth.User)) {
		mux.HandleFunc(pattern, s.guard(pol, h))
	}
	api("GET /api/me", anyUser, s.me)
	api("GET /api/health", anyUser, s.health)
	if s.admin != nil {
		api("GET /api/proxy/config", globalRead, s.proxyConfig)
		api("GET /api/proxy/keys", team, s.proxyListKeys)
		api("POST /api/proxy/keys", team, s.proxyMintKey)
		api("DELETE /api/proxy/keys/{name}", team, s.proxyRevokeKey)
		api("GET /api/proxy/nodes", globalRead, s.proxyListNodes)
		api("POST /api/proxy/nodes/{id}/drain", adminOnly, s.proxyDrainNode)
		api("GET /api/proxy/node-keys", globalRead, s.proxyListNodeKeys)
		api("POST /api/proxy/node-keys", adminOnly, s.proxyMintNodeKey)
		api("POST /api/proxy/node-keys/revoke", adminOnly, s.proxyRevokeNodeKey)
		api("PUT /api/proxy/models/{id}", adminOnly, s.proxyUpsertModel)
		api("DELETE /api/proxy/models/{id}", adminOnly, s.proxyDeleteModel)
		api("PUT /api/proxy/rules", adminOnly, s.proxyReplaceRules)
		api("GET /api/proxy/user-rate-limits", globalRead, s.proxyListUserRateLimits)
		api("PUT /api/proxy/user-rate-limits", adminOnly, s.proxySetUserRateLimit)
		api("DELETE /api/proxy/user-rate-limits", adminOnly, s.proxyDeleteUserRateLimit)
		api("GET /api/proxy/spend-limits", globalRead, s.proxyListSpendLimits)
		api("PUT /api/proxy/spend-limits", adminOnly, s.proxySetSpendLimit)
		api("DELETE /api/proxy/spend-limits", adminOnly, s.proxyDeleteSpendLimit)
		api("PUT /api/proxy/vendor-keys", adminOnly, s.proxySetVendorKey)
		api("DELETE /api/proxy/vendor-keys", adminOnly, s.proxyDeleteVendorKey)
		api("GET /api/proxy/organizations", globalRead, s.proxyListOrganizations)
		api("POST /api/proxy/organizations", adminOnly, s.proxyCreateOrganization)
		api("DELETE /api/proxy/organizations/{id}", adminOnly, s.proxyDeleteOrganization)
		api("GET /api/proxy/teams", team, s.proxyListTeams)
		api("POST /api/proxy/teams", adminOnly, s.proxyCreateTeam)
		api("GET /api/proxy/teams/{id}", team, s.proxyGetTeam)
		api("PUT /api/proxy/teams/{id}", adminOnly, s.proxyUpdateTeam)
		api("DELETE /api/proxy/teams/{id}", adminOnly, s.proxyDeleteTeam)
		api("GET /api/proxy/teams/{id}/cabinet", team, s.proxyTeamCabinet)
		api("GET /api/proxy/team-spend", globalRead, s.proxyTeamsSpend)
		api("POST /api/proxy/rotate", adminOnly, s.proxyRotate)
		// The console's own gateway key runs these, outside any team's models and budget.
		api("POST /api/proxy/playground", adminOnly, s.proxyPlayground)
		api("POST /api/proxy/playground/stream", adminOnly, s.proxyPlaygroundStream)
		api("POST /api/proxy/judge", adminOnly, s.proxyJudge)
	}
	if s.usage != nil {
		api("GET /api/usage", globalRead, s.usageReport)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "no such endpoint (is that backend configured?)")
	})

	files := http.FileServerFS(web.FS())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			if _, ok := s.auth.UserFor(r); !ok {
				http.Redirect(w, r, "/auth/login?next=/", http.StatusFound)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://*.googleusercontent.com; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type handler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// policy is who may reach a route at all; handlers of team routes then narrow
// a team user to their own team. Anything not listed is denied.
type policy int

const (
	anyUser    policy = iota // /api/me, /api/health
	globalRead               // every tenant's data: admins and viewers
	adminOnly                // global changes, and calls made with the console's own key
	team                     // admins and viewers globally, team users for their own team
)

func (p policy) allows(u *auth.User, write bool) bool {
	switch p {
	case anyUser:
		return !write
	case globalRead:
		return !write && u.CanReadGlobal()
	case adminOnly:
		return u.IsSuperAdmin()
	case team:
		if write {
			return u.IsSuperAdmin() || u.IsTeamLead()
		}
		return u.CanReadGlobal() || u.InTeam()
	}
	return false
}

// guard requires a session, the route's policy and, for writes, a same-origin
// Origin (when sent) and a CSRF header a cross-site form cannot set.
func (s *Server) guard(pol policy, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.auth.UserFor(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthenticated", "sign in first")
			return
		}
		write := r.Method != http.MethodGet && r.Method != http.MethodHead
		if !pol.allows(u, write) {
			writeErr(w, http.StatusForbidden, "forbidden", "your account may not do this")
			return
		}
		if write {
			if r.Header.Get(csrfHeader) != csrfValue {
				writeErr(w, http.StatusForbidden, "csrf", "missing "+csrfHeader+" header")
				return
			}
			if o := r.Header.Get("Origin"); o != "" && s.cfg.PublicURL != "" && o != s.cfg.PublicURL {
				writeErr(w, http.StatusForbidden, "csrf", "cross-origin request refused")
				return
			}
		}
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Str("path", r.URL.Path).Msg("handler panic")
				writeErr(w, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		h(w, r, u)
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": kind, "message": msg}})
}

func writeProto(w http.ResponseWriter, m proto.Message) {
	b, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "cannot encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// protoValue encodes m the way writeProto does, for embedding in a larger reply.
func protoValue(m proto.Message) (map[string]any, error) {
	b, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	return v, json.Unmarshal(b, &v)
}

func readProto(r *http.Request, m proto.Message) error {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: false}.Unmarshal(raw, m)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// backendErr turns a gRPC error from the proxy into an HTTP error. The proxy's
// messages are written for operators (validation reasons), so they pass through.
func backendErr(w http.ResponseWriter, err error) {
	var perr *protoErr
	if errors.As(err, &perr) {
		writeErr(w, http.StatusBadRequest, "bad_request", perr.Error())
		return
	}
	st, _ := status.FromError(err)
	code := http.StatusBadGateway
	switch st.Code() {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.AlreadyExists, codes.OutOfRange:
		code = http.StatusBadRequest
	case codes.NotFound:
		code = http.StatusNotFound
	case codes.Unauthenticated:
		code = http.StatusBadGateway // the console's own key is wrong: not the caller's fault
	case codes.PermissionDenied:
		code = http.StatusBadGateway
	case codes.ResourceExhausted:
		code = http.StatusTooManyRequests
	case codes.Unavailable, codes.DeadlineExceeded:
		code = http.StatusServiceUnavailable
	}
	msg := st.Message()
	if st.Code() == codes.Unauthenticated || st.Code() == codes.PermissionDenied {
		msg = "the KTMS gateway rejected the console's admin key: " + msg
	}
	if st.Code() == codes.Unavailable {
		msg = "the KTMS gateway is unreachable: " + msg
	}
	log.Warn().Str("grpc", st.Code().String()).Str("msg", st.Message()).Msg("backend error")
	writeErr(w, code, strings.ToLower(st.Code().String()), msg)
}

type protoErr struct{ error }

func badBody(err error) error { return &protoErr{error: err} }

// ---- identity & health ----

func (s *Server) me(w http.ResponseWriter, _ *http.Request, u *auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":     u,
		"features": map[string]bool{"proxy": s.admin != nil, "usage": s.usage != nil},
	})
}

type backendStatus struct {
	Enabled bool   `json:"enabled"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	out := map[string]backendStatus{"proxy": {}, "usage": {}}
	if s.admin != nil {
		st := backendStatus{Enabled: true, OK: true}
		if _, err := s.admin.ListAPIKeys(ctx, &pb.ListAPIKeysRequest{}); err != nil {
			st.OK, st.Error = false, status.Convert(err).Message()
		}
		out["proxy"] = st
	}
	if s.usage != nil {
		st := backendStatus{Enabled: true, OK: true}
		if err := s.usage.Ready(ctx); err != nil {
			st.OK, st.Error = false, err.Error()
		}
		out["usage"] = st
	}
	writeJSON(w, http.StatusOK, out)
}
