// Package cfaccess identifies users by the token Cloudflare Access adds to
// every request it lets through to the console.
package cfaccess

import (
	"context"
	"errors"
	"net/http"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
)

const (
	methodID = "cloudflare-access"
	tokenHdr = "Cf-Access-Jwt-Assertion"
)

type Options struct {
	// TeamDomain is "myteam", "myteam.cloudflareaccess.com" or a full URL.
	TeamDomain string
	// Audience is the application's AUD tag.
	Audience string
}

// Method checks the token's signature, issuer, audience and expiry, so unlike
// the header method it needs no trusted network path.
type Method struct {
	verifier *gooidc.IDTokenVerifier
}

func New(o Options) (*Method, error) {
	if o.TeamDomain == "" || o.Audience == "" {
		return nil, errors.New("CF_ACCESS_TEAM_DOMAIN and CF_ACCESS_AUD are both required for Cloudflare Access")
	}
	issuer := Issuer(o.TeamDomain)
	// The key set outlives any request, so it fetches with its own context.
	keys := gooidc.NewRemoteKeySet(context.Background(), issuer+"/cdn-cgi/access/certs")
	return &Method{verifier: gooidc.NewVerifier(issuer, keys, &gooidc.Config{ClientID: o.Audience})}, nil
}

// Issuer turns the team domain into the token issuer, https://<team>.cloudflareaccess.com.
func Issuer(team string) string {
	team = strings.TrimRight(strings.TrimSpace(team), "/")
	if strings.Contains(team, "://") {
		return team
	}
	if !strings.Contains(team, ".") {
		team += ".cloudflareaccess.com"
	}
	return "https://" + team
}

func (m *Method) ID() string { return methodID }

func (m *Method) Identify(r *http.Request) (auth.Identity, bool) {
	raw := r.Header.Get(tokenHdr)
	if raw == "" {
		return auth.Identity{}, false
	}
	tok, err := m.verifier.Verify(r.Context(), raw)
	if err != nil {
		log.Debug().Err(err).Msg("Cloudflare Access token rejected")
		return auth.Identity{}, false
	}
	// Service tokens carry no e-mail; they are for machines, not this console.
	var claims struct {
		Email string `json:"email"`
	}
	if tok.Claims(&claims) != nil || claims.Email == "" {
		return auth.Identity{}, false
	}
	return auth.Identity{Email: claims.Email}, true
}
