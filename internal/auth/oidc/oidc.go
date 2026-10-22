// Package oidc signs users in with an OpenID Connect provider, Google by default.
package oidc

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
)

const (
	methodID     = "oidc"
	googleIssuer = "https://accounts.google.com"
	startPath    = "/auth/oidc/start"
	// CallbackPath is the redirect URI path registered at the provider.
	CallbackPath = "/auth/callback"
)

type Options struct {
	// Issuer is the provider's issuer URL; empty means Google.
	Issuer       string
	ClientID     string
	ClientSecret string
}

// Method runs the authorization-code flow with PKCE, state and nonce, and
// accepts only verified e-mails.
type Method struct {
	o    Options
	host auth.Host

	mu       sync.Mutex
	provider *gooidc.Provider
}

func New(o Options) *Method {
	if o.Issuer == "" {
		o.Issuer = googleIssuer
	}
	return &Method{o: o}
}

type flowData struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"u"`
}

func (m *Method) ID() string { return methodID }

func (m *Method) Label() string {
	if m.isGoogle() {
		return "Sign in with Google"
	}
	return "Sign in with SSO"
}

func (m *Method) StartURL(next string) string {
	return startPath + "?next=" + url.QueryEscape(next)
}

func (m *Method) Mount(mux *http.ServeMux, host auth.Host) {
	m.host = host
	mux.HandleFunc("GET "+startPath, m.start)
	mux.HandleFunc("GET "+CallbackPath, m.callback)
}

func (m *Method) isGoogle() bool { return m.o.Issuer == googleIssuer }

// providerName is for error messages.
func (m *Method) providerName() string {
	if m.isGoogle() {
		return "Google"
	}
	return "the identity provider"
}

func (m *Method) oauthConfig(ctx context.Context) (*oauth2.Config, *gooidc.Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.provider == nil {
		p, err := gooidc.NewProvider(ctx, m.o.Issuer)
		if err != nil {
			return nil, nil, fmt.Errorf("reach %s: %w", m.o.Issuer, err)
		}
		m.provider = p
	}
	return &oauth2.Config{
		ClientID:     m.o.ClientID,
		ClientSecret: m.o.ClientSecret,
		RedirectURL:  m.host.PublicURL() + CallbackPath,
		Endpoint:     m.provider.Endpoint(),
		Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
	}, m.provider, nil
}

func (m *Method) unreachable(w http.ResponseWriter) {
	m.host.Fail(w, http.StatusBadGateway, "Cannot reach "+m.providerName()+" right now. Try again in a moment.")
}

func (m *Method) start(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := m.oauthConfig(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("login")
		m.unreachable(w)
		return
	}
	f := flowData{State: rand.Text(), Nonce: rand.Text(), Verifier: oauth2.GenerateVerifier(),
		Next: auth.SafeNext(r.URL.Query().Get("next"))}
	if err := m.host.SaveFlow(w, methodID, f); err != nil {
		m.host.Fail(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	dest := cfg.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier),
		gooidc.Nonce(f.Nonce), oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, dest, http.StatusFound)
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
		m.host.Fail(w, http.StatusForbidden, "Sign-in with "+m.providerName()+" was cancelled or refused.")
		return
	}
	cfg, provider, err := m.oauthConfig(r.Context())
	if err != nil {
		m.unreachable(w)
		return
	}
	tok, err := cfg.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		log.Warn().Err(err).Msg("code exchange failed")
		m.host.Fail(w, http.StatusBadGateway, "Sign-in with "+m.providerName()+" failed. Try again.")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idToken, err := provider.Verifier(&gooidc.Config{ClientID: m.o.ClientID}).Verify(r.Context(), rawID)
	if err != nil || idToken.Nonce != f.Nonce {
		log.Warn().Err(err).Msg("id token rejected")
		m.host.Fail(w, http.StatusForbidden, "Sign-in with "+m.providerName()+" could not be verified.")
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil || !claims.EmailVerified {
		m.host.Fail(w, http.StatusForbidden, "Your e-mail address is not verified by "+m.providerName()+".")
		return
	}
	m.host.SignIn(w, r, methodID, auth.Identity{Email: claims.Email, Name: claims.Name, Picture: claims.Picture}, f.Next)
}
