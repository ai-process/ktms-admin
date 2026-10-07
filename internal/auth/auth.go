package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

const (
	sessionCookie = "lpa_session"
	flowCookie    = "lpa_login"
	flowTTL       = 10 * time.Minute

	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// User is the authenticated caller.
type User struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	Role    string `json:"role"`
}

func (u *User) CanWrite() bool { return u.Role == RoleAdmin }

type sessionData struct {
	Email   string `json:"e"`
	Name    string `json:"n"`
	Picture string `json:"p"`
	Exp     int64  `json:"x"`
}

type flowData struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"u"`
	Exp      int64  `json:"x"`
}

type Options struct {
	PublicURL      string
	ClientID       string
	ClientSecret   string
	Admins         *Allowlist
	Viewers        *Allowlist
	Signer         *Signer
	SessionTTL     time.Duration
	SecureCookies  bool
	InsecureNoAuth bool
	// Issuer is the OIDC issuer; empty means Google.
	Issuer string
}

// Handler runs the Google sign-in flow and resolves sessions to users.
type Handler struct {
	o Options

	mu       sync.Mutex
	provider *oidc.Provider
}

func New(o Options) *Handler { return &Handler{o: o} }

func (h *Handler) issuer() string {
	if h.o.Issuer != "" {
		return h.o.Issuer
	}
	return "https://accounts.google.com"
}

func (h *Handler) oauthConfig(ctx context.Context) (*oauth2.Config, *oidc.Provider, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.provider == nil {
		p, err := oidc.NewProvider(ctx, h.issuer())
		if err != nil {
			return nil, nil, fmt.Errorf("reach Google: %w", err)
		}
		h.provider = p
	}
	return &oauth2.Config{
		ClientID:     h.o.ClientID,
		ClientSecret: h.o.ClientSecret,
		RedirectURL:  h.o.PublicURL + "/auth/callback",
		Endpoint:     h.provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}, h.provider, nil
}

// roleFor is evaluated on every request so list changes apply at once.
func (h *Handler) roleFor(email string) string {
	switch {
	case h.o.Admins.Match(email):
		return RoleAdmin
	case h.o.Viewers.Match(email):
		return RoleViewer
	}
	return ""
}

// UserFor returns the signed-in user for a request, if any.
func (h *Handler) UserFor(r *http.Request) (*User, bool) {
	if h.o.InsecureNoAuth {
		return &User{Email: "dev@localhost", Name: "Local dev", Role: RoleAdmin}, true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	var s sessionData
	if err := h.o.Signer.Open(c.Value, &s); err != nil || time.Now().Unix() >= s.Exp {
		return nil, false
	}
	role := h.roleFor(s.Email)
	if role == "" {
		return nil, false
	}
	return &User{Email: s.Email, Name: s.Name, Picture: s.Picture, Role: role}, true
}

func randString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeNext only lets same-site absolute paths through, never an open redirect.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func (h *Handler) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: h.o.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.o.SecureCookies, SameSite: http.SameSiteLaxMode})
}

// Login starts the authorization-code flow with PKCE, state and nonce.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.o.InsecureNoAuth {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	cfg, _, err := h.oauthConfig(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("login")
		denyPage(w, http.StatusBadGateway, "Cannot reach Google right now. Try again in a moment.")
		return
	}
	f := flowData{State: randString(24), Nonce: randString(24), Verifier: oauth2.GenerateVerifier(),
		Next: safeNext(r.URL.Query().Get("next")), Exp: time.Now().Add(flowTTL).Unix()}
	sealed, err := h.o.Signer.Seal(f)
	if err != nil {
		denyPage(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	h.setCookie(w, flowCookie, sealed, flowTTL)
	url := cfg.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier),
		oidc.Nonce(f.Nonce), oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback completes the flow, applies the allowlist and starts a session.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(flowCookie)
	var f flowData
	if err != nil || h.o.Signer.Open(c.Value, &f) != nil || time.Now().Unix() >= f.Exp {
		denyPage(w, http.StatusBadRequest, "The sign-in attempt expired. Start again.")
		return
	}
	h.clearCookie(w, flowCookie)
	q := r.URL.Query()
	if q.Get("state") == "" || q.Get("state") != f.State {
		denyPage(w, http.StatusBadRequest, "Sign-in state mismatch. Start again.")
		return
	}
	if e := q.Get("error"); e != "" {
		denyPage(w, http.StatusForbidden, "Google sign-in was cancelled or refused.")
		return
	}
	cfg, provider, err := h.oauthConfig(r.Context())
	if err != nil {
		denyPage(w, http.StatusBadGateway, "Cannot reach Google right now. Try again in a moment.")
		return
	}
	tok, err := cfg.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		log.Warn().Err(err).Msg("code exchange failed")
		denyPage(w, http.StatusBadGateway, "Google sign-in failed. Try again.")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idToken, err := provider.Verifier(&oidc.Config{ClientID: h.o.ClientID}).Verify(r.Context(), rawID)
	if err != nil || idToken.Nonce != f.Nonce {
		log.Warn().Err(err).Msg("id token rejected")
		denyPage(w, http.StatusForbidden, "Google sign-in could not be verified.")
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil || !claims.EmailVerified {
		denyPage(w, http.StatusForbidden, "Your Google e-mail address is not verified.")
		return
	}
	if h.roleFor(claims.Email) == "" {
		log.Warn().Str("email", claims.Email).Msg("sign-in refused: not on the allowlist")
		denyPage(w, http.StatusForbidden, html.EscapeString(claims.Email)+" is not allowed to use this console.")
		return
	}
	sealed, err := h.o.Signer.Seal(sessionData{Email: claims.Email, Name: claims.Name, Picture: claims.Picture,
		Exp: time.Now().Add(h.o.SessionTTL).Unix()})
	if err != nil {
		denyPage(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	h.setCookie(w, sessionCookie, sealed, h.o.SessionTTL)
	log.Info().Str("email", claims.Email).Str("role", h.roleFor(claims.Email)).Msg("signed in")
	http.Redirect(w, r, f.Next, http.StatusFound)
}

// Logout drops the session. POST only, so a link or image cannot sign you out.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.clearCookie(w, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}

func denyPage(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>Sign-in</title><style>body{font:16px system-ui;max-width:28rem;margin:18vh auto;padding:0 1rem;color:#222}
a{color:#2b59c3}@media(prefers-color-scheme:dark){body{background:#14161a;color:#e6e6e6}a{color:#8ab4ff}}</style>
<h1>Sign-in</h1><p>%s</p><p><a href="/auth/login">Try again</a></p>`, msg)
}
