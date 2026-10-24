package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/ai-process/ktms-admin/internal/gen/ktms/v1"
)

// scopedAdmin answers as Keystone would for the key a call carries: the admin
// key may do anything, the read key only read.
type scopedAdmin struct {
	*fakeAdmin
	key   func(context.Context, string) string
	admin string
	read  string
	mu    sync.Mutex        // the health probes call concurrently
	used  map[string]string // method → key it carried
	mints int
	down  bool
}

func (f *scopedAdmin) check(ctx context.Context, method string, write bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return status.Error(codes.Unavailable, "connection refused")
	}
	k := f.key(ctx, method)
	f.used[method] = k
	switch {
	case k == f.admin:
		return nil
	case k == f.read && !write:
		return nil
	case k == f.read:
		return status.Error(codes.PermissionDenied, "apikeys: insufficient scope: need admin")
	}
	return status.Error(codes.Unauthenticated, "invalid key")
}

func (f *scopedAdmin) ListAPIKeys(ctx context.Context, r *pb.ListAPIKeysRequest, o ...grpc.CallOption) (*pb.ListAPIKeysResponse, error) {
	if err := f.check(ctx, "/ktms.v1.AdminService/ListAPIKeys", false); err != nil {
		return nil, err
	}
	return f.fakeAdmin.ListAPIKeys(ctx, r, o...)
}

func (f *scopedAdmin) MintAPIKey(ctx context.Context, r *pb.MintAPIKeyRequest, o ...grpc.CallOption) (*pb.MintAPIKeyResponse, error) {
	f.mu.Lock()
	f.mints++
	f.mu.Unlock()
	if err := f.check(ctx, "/ktms.v1.AdminService/MintAPIKey", true); err != nil {
		return nil, err
	}
	if r.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	return f.fakeAdmin.MintAPIKey(ctx, r, o...)
}

func scoped(t *testing.T, readKey string) (*Server, *scopedAdmin) {
	t.Helper()
	s, fa, _ := newServer(t, "")
	s.cfg.ProxyAdminKey, s.cfg.ProxyReadKey = "ktms_admin", readKey
	sa := &scopedAdmin{fakeAdmin: fa, key: s.keyFor, admin: "ktms_admin", read: "ktms_read", used: map[string]string{}}
	s.admin = sa
	return s, sa
}

func TestReadKeyCarriesReadsAndViewers(t *testing.T) {
	s, sa := scoped(t, "ktms_read")
	h := s.Handler()
	viewer, boss := sessionCookie(t, "p@view.com"), sessionCookie(t, "boss@x.com")

	if w := call(h, "GET", "/api/proxy/keys", "", boss, nil); w.Code != 200 || sa.used["/ktms.v1.AdminService/ListAPIKeys"] != "ktms_read" {
		t.Fatalf("an admin's read went with %q (%d)", sa.used["/ktms.v1.AdminService/ListAPIKeys"], w.Code)
	}
	if w := call(h, "POST", "/api/proxy/keys", `{"name":"svc","scopes":["generate"]}`, boss, csrf); w.Code != 200 || sa.used["/ktms.v1.AdminService/MintAPIKey"] != "ktms_admin" {
		t.Fatalf("an admin's change went with %q (%d %s)", sa.used["/ktms.v1.AdminService/MintAPIKey"], w.Code, w.Body)
	}
	if w := call(h, "GET", "/api/proxy/keys", "", viewer, nil); w.Code != 200 || sa.used["/ktms.v1.AdminService/ListAPIKeys"] != "ktms_read" {
		t.Fatalf("a viewer's read went with %q", sa.used["/ktms.v1.AdminService/ListAPIKeys"])
	}

	// Whatever a viewer's call is, it carries the read key.
	ctx := context.WithValue(context.Background(), viewerCtx{}, true)
	if k := s.keyFor(ctx, "/ktms.v1.AdminService/RevokeAPIKey"); k != "ktms_read" {
		t.Fatalf("a viewer's change would go with %q", k)
	}
	for m, want := range map[string]string{
		"/ktms.v1.AdminService/GetConfig":      "ktms_read",
		"/ktms.v1.AdminService/ExplainRoute":   "ktms_read",
		"/ktms.v1.AdminService/ReplaceRules":   "ktms_admin",
		"/ktms.v1.GatewayService/GenerateText": "ktms_admin",
	} {
		if k := s.keyFor(context.Background(), m); k != want {
			t.Errorf("%s goes with %q, want %q", m, k, want)
		}
	}

	// The interceptor puts the chosen key on the wire.
	var got string
	_ = attachKey(s.keyFor)(context.Background(), "/ktms.v1.AdminService/ListNodes", nil, nil, nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			got = md.Get("authorization")[0]
			return nil
		})
	if got != "Bearer ktms_read" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestWithoutReadKeyEverythingUsesTheAdminKey(t *testing.T) {
	s, _ := scoped(t, "")
	ctx := context.WithValue(context.Background(), viewerCtx{}, true)
	for _, m := range []string{"/ktms.v1.AdminService/ListAPIKeys", "/ktms.v1.AdminService/MintAPIKey"} {
		if k := s.keyFor(ctx, m); k != "ktms_admin" {
			t.Errorf("%s goes with %q", m, k)
		}
	}
}

func TestHealthChecksBothKeys(t *testing.T) {
	health := func(s *Server) backendStatus {
		t.Helper()
		w := call(s.Handler(), "GET", "/api/health", "", sessionCookie(t, "boss@x.com"), nil)
		var out map[string]backendStatus
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out["proxy"]
	}
	s, _ := scoped(t, "ktms_read")
	if st := health(s); !st.OK || len(st.Keys) != 2 || !st.Keys[0].OK || !st.Keys[1].OK {
		t.Fatalf("both keys right: %+v", st)
	}

	// The read key in KTMS_ADMIN_KEY: reads work, the console cannot change anything.
	s, sa := scoped(t, "")
	sa.admin = "nobody"
	s.cfg.ProxyAdminKey = "ktms_read"
	if st := health(s); st.OK || !st.Auth || st.Keys[0].OK {
		t.Fatalf("a read key as the admin key: %+v", st)
	}

	// An admin key in KTMS_READ_KEY is reported: it is not read-only.
	s, sa = scoped(t, "ktms_admin")
	if st := health(s); st.OK || st.Keys[1].OK || !st.Keys[1].Refused {
		t.Fatalf("an admin key as the read key: %+v", st)
	}
	// The probes are cached: Keystone sees them once a minute, not per page.
	before := sa.mints
	health(s)
	if sa.mints != before {
		t.Fatalf("keys probed again within %s", keyCheckTTL)
	}

	// A gateway that is down is down, not a refused key.
	s, sa = scoped(t, "ktms_read")
	sa.down = true
	if st := health(s); st.OK || st.Auth || st.Error == "" {
		t.Fatalf("a gateway that is down: %+v", st)
	}
}
