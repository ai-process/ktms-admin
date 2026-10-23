// Package github signs users in with GitHub (OAuth2, not OpenID Connect),
// using the account's primary verified e-mail.
package github

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"

	"github.com/ai-process/ktms-admin/internal/auth"
)

const (
	methodID  = "github"
	startPath = "/auth/github/start"
	// CallbackPath is the callback URL path to register in the OAuth app.
	CallbackPath = "/auth/github/callback"
)

type Options struct {
	ClientID     string
	ClientSecret string
	// BaseURL is GitHub Enterprise Server's address; empty means github.com.
	BaseURL string
}

type Method struct {
	cfg    oauth2.Config
	apiURL string
	host   auth.Host
	client *http.Client
}

func New(o Options) *Method {
	base := strings.TrimRight(o.BaseURL, "/")
	api := base + "/api/v3"
	if base == "" || base == "https://github.com" {
		base, api = "https://github.com", "https://api.github.com"
	}
	return &Method{
		cfg: oauth2.Config{
			ClientID: o.ClientID, ClientSecret: o.ClientSecret,
			Endpoint: oauth2.Endpoint{AuthURL: base + "/login/oauth/authorize", TokenURL: base + "/login/oauth/access_token"},
			Scopes:   []string{"read:user", "user:email"},
		},
		apiURL: api,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type flowData struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"u"`
}

func (m *Method) ID() string    { return methodID }
func (m *Method) Label() string { return "Sign in with GitHub" }

func (m *Method) StartURL(next string) string {
	return startPath + "?next=" + url.QueryEscape(next)
}

func (m *Method) Mount(mux *http.ServeMux, host auth.Host) {
	m.host = host
	m.cfg.RedirectURL = host.PublicURL() + CallbackPath
	mux.HandleFunc("GET "+startPath, m.start)
	mux.HandleFunc("GET "+CallbackPath, m.callback)
}

func (m *Method) start(w http.ResponseWriter, r *http.Request) {
	f := flowData{State: rand.Text(), Verifier: oauth2.GenerateVerifier(), Next: auth.SafeNext(r.URL.Query().Get("next"))}
	if err := m.host.SaveFlow(w, methodID, f); err != nil {
		m.host.Fail(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	http.Redirect(w, r, m.cfg.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier)), http.StatusFound)
}

func (m *Method) callback(w http.ResponseWriter, r *http.Request) {
	var f flowData
	if !m.host.TakeFlow(w, r, methodID, &f) {
		m.host.Fail(w, http.StatusBadRequest, "The sign-in attempt expired. Start again.")
		return
	}
	q := r.URL.Query()
	if q.Get("state") == "" || q.Get("state") != f.State {
		m.host.Fail(w, http.StatusBadRequest, "Sign-in state mismatch. Start again.")
		return
	}
	if q.Get("error") != "" {
		m.host.Fail(w, http.StatusForbidden, "Sign-in with GitHub was cancelled or refused.")
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, m.client)
	tok, err := m.cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		log.Warn().Err(err).Msg("GitHub code exchange failed")
		m.host.Fail(w, http.StatusBadGateway, "Sign-in with GitHub failed. Try again.")
		return
	}
	id, err := m.identity(ctx, tok.AccessToken)
	if err != nil {
		log.Warn().Err(err).Msg("GitHub identity")
		m.host.Fail(w, http.StatusForbidden, err.Error())
		return
	}
	m.host.SignIn(w, r, methodID, id, f.Next)
}

// identity reads the primary verified e-mail; the profile only adds a name
// and an avatar, so it may fail.
func (m *Method) identity(ctx context.Context, token string) (auth.Identity, error) {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := m.get(ctx, token, "/user/emails", &emails); err != nil {
		return auth.Identity{}, errors.New("Could not read your GitHub e-mail addresses. Try again.")
	}
	var id auth.Identity
	for _, e := range emails {
		if e.Primary && e.Verified {
			id.Email = e.Email
		}
	}
	if id.Email == "" {
		return id, errors.New("Your primary GitHub e-mail address is not verified.")
	}
	var user struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := m.get(ctx, token, "/user", &user); err == nil {
		id.Name, id.Picture = user.Name, user.AvatarURL
		if id.Name == "" {
			id.Name = user.Login
		}
	}
	return id, nil
}

func (m *Method) get(ctx context.Context, token, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", m.apiURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}
