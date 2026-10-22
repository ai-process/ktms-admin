// Package oidc signs users in with an OpenID Connect provider (Google,
// Keycloak, Zitadel, Entra ID, GitLab, …), Google by default.
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

	"github.com/ai-process/ktms-admin/internal/auth"
)

const (
	// DefaultID is the unnamed provider. It keeps /auth/callback, the redirect
	// URI registered before there could be several.
	DefaultID    = "oidc"
	googleIssuer = "https://accounts.google.com"
)

type Options struct {
	// ID names the method and its routes; empty means DefaultID.
	ID string
	// Issuer is the provider's issuer URL; empty means Google.
	Issuer       string
	ClientID     string
	ClientSecret string
	// Name is shown as "Sign in with <Name>"; empty means Google or SSO.
	Name string
}

// Method runs the authorization-code flow with PKCE, state and nonce, and
// accepts only verified e-mails.
type Method struct {
	o    Options
	host auth.Host
	// errName is the provider in error messages, where "SSO" reads badly.
	errName string

	mu       sync.Mutex
	provider *gooidc.Provider
}

func New(o Options) *Method {
	if o.ID == "" {
		o.ID = DefaultID
	}
	if o.Issuer == "" {
		o.Issuer = googleIssuer
	}
	m := &Method{o: o, errName: o.Name}
	switch {
	case o.Name != "":
	case o.Issuer == googleIssuer:
		m.o.Name, m.errName = "Google", "Google"
	default:
		m.o.Name, m.errName = "SSO", "the identity provider"
	}
	return m
}

// CallbackPath is the redirect URI path to register at the provider.
func (m *Method) CallbackPath() string {
	if m.o.ID == DefaultID {
		return "/auth/callback"
	}
	return "/auth/" + m.o.ID + "/callback"
}

func (m *Method) startPath() string { return "/auth/" + m.o.ID + "/start" }

type flowData struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"u"`
}

func (m *Method) ID() string    { return m.o.ID }
func (m *Method) Label() string { return "Sign in with " + m.o.Name }

func (m *Method) StartURL(next string) string {
	return m.startPath() + "?next=" + url.QueryEscape(next)
}

func (m *Method) Mount(mux *http.ServeMux, host auth.Host) {
	m.host = host
	mux.HandleFunc("GET "+m.startPath(), m.start)
	mux.HandleFunc("GET "+m.CallbackPath(), m.callback)
}

// providerName is for error messages.
func (m *Method) providerName() string { return m.errName }

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
		RedirectURL:  m.host.PublicURL() + m.CallbackPath(),
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
	if err := m.host.SaveFlow(w, m.o.ID, f); err != nil {
		m.host.Fail(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	dest := cfg.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier),
		gooidc.Nonce(f.Nonce), oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, dest, http.StatusFound)
}

func (m *Method) callback(w http.ResponseWriter, r *http.Request) {
	var f flowData
	if !m.host.TakeFlow(w, r, m.o.ID, &f) {
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
	m.host.SignIn(w, r, m.o.ID, auth.Identity{Email: claims.Email, Name: claims.Name, Picture: claims.Picture}, f.Next)
}
