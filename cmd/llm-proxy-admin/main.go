package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/ai-process/llm-proxy-admin/internal/api"
	"github.com/ai-process/llm-proxy-admin/internal/auth"
	"github.com/ai-process/llm-proxy-admin/internal/auth/cfaccess"
	"github.com/ai-process/llm-proxy-admin/internal/auth/header"
	"github.com/ai-process/llm-proxy-admin/internal/auth/oidc"
	"github.com/ai-process/llm-proxy-admin/internal/auth/password"
	"github.com/ai-process/llm-proxy-admin/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		os.Exit(hashPassword(os.Args[2:]))
	}
	log.Logger = log.Output(os.Stderr)
	cfg, err := config.Load()
	if cfg != nil {
		if lvl, perr := zerolog.ParseLevel(cfg.LogLevel); perr == nil {
			zerolog.SetGlobalLevel(lvl)
		}
		// Before any fatal error, so an operator on the old names sees how
		// their settings map to the message.
		for _, n := range cfg.Notices {
			log.Warn().Msg(n)
		}
	}
	if err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	admins, err := auth.ParseAllowlist(cfg.AllowedEmails)
	if err != nil {
		log.Fatal().Err(err).Msg("ALLOWED_EMAILS")
	}
	viewers, err := auth.ParseAllowlist(cfg.ViewerEmails)
	if err != nil {
		log.Fatal().Err(err).Msg("VIEWER_EMAILS")
	}
	if admins.AllowsAll() || viewers.AllowsAll() {
		log.Warn().Msg("an allowlist contains *@*: everyone who can sign in gets access")
	}
	secret := cfg.SessionSecret
	switch {
	case secret == "":
		secret = auth.RandomSecret()
		log.Warn().Msg("SESSION_SECRET unset: using a random one, so sessions end on every restart and cannot be shared between replicas")
	case len(secret) < 32:
		log.Fatal().Msg("SESSION_SECRET must be at least 32 characters")
	}
	if cfg.InsecureNoAuth {
		log.Warn().Msg("INSECURE_NO_AUTH is on: local dev mode without authentication")
	}

	memberships := auth.ParseTeamMemberships(cfg.TeamMemberships)

	methods, err := signInMethods(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("sign-in")
	}
	handler := auth.New(auth.Options{
		PublicURL: cfg.PublicURL, Methods: methods,
		Admins: admins, Viewers: viewers, TeamMemberships: memberships,
		DevUserRole: cfg.DevUserRole, DevUserTeamID: cfg.DevUserTeamID, DevUserOrgID: cfg.DevUserOrgID,
		Signer: auth.NewSigner(secret), SessionTTL: cfg.SessionTTL,
		SecureCookies: cfg.SecureCookies(), InsecureNoAuth: cfg.InsecureNoAuth,
	})
	srv, closeBackends, err := api.New(cfg, handler)
	if err != nil {
		log.Fatal().Err(err).Msg("backends")
	}
	defer closeBackends()

	httpSrv := &http.Server{
		Addr: cfg.ListenAddr, Handler: srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 15 * time.Minute, IdleTimeout: 2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info().Str("addr", cfg.ListenAddr).Bool("proxy", cfg.ProxyEnabled()).Bool("usage", cfg.UsageEnabled()).
			Int("admins", admins.Len()).Int("viewers", viewers.Len()).Msg("ktms-admin started")
		errCh <- httpSrv.ListenAndServe()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Info().Str("signal", s.String()).Msg("shutting down")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("server failed")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

// signInMethods builds the methods the configuration turns on; order is the
// order of the buttons on the sign-in page.
func signInMethods(cfg *config.Config) ([]auth.Method, error) {
	var methods []auth.Method
	if cfg.PasswordEnabled() {
		users, err := password.LoadUsers(cfg.AdminUsers, cfg.AdminUsersFile)
		if err != nil {
			return nil, err
		}
		m, err := password.New(users)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_USERS: %w", err)
		}
		methods = append(methods, m)
	}
	if cfg.GoogleEnabled() {
		methods = append(methods, oidc.New(oidc.Options{Issuer: cfg.OIDCIssuer, ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret}))
	}
	if cfg.HeaderEnabled() {
		m, err := header.New(header.Options{TrustedProxies: cfg.TrustedProxyCIDRs, EmailHeader: cfg.AuthHeaderEmail, NameHeader: cfg.AuthHeaderName})
		if err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	if cfg.CFAccessEnabled() {
		m, err := cfaccess.New(cfaccess.Options{TeamDomain: cfg.CFAccessTeamDomain, Audience: cfg.CFAccessAUD})
		if err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	for _, m := range methods {
		log.Info().Str("method", m.ID()).Msg("sign-in method on")
	}
	return methods, nil
}
