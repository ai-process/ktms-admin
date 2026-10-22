// Package password signs users in with an e-mail and a password hashed in
// the configuration, so the console needs no identity provider and no database.
package password

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
)

const (
	methodID = "password"
	postPath = "/auth/password"
	maxField = 1024
)

type Method struct {
	users map[string]string // lower-case e-mail -> hash
	host  auth.Host
	// decoy is verified for unknown e-mails so they take as long as known ones.
	decoy string
	// slots bounds concurrent hashing: each argon2id run holds 19 MiB.
	slots    chan struct{}
	byEmail  *limiter
	byClient *limiter
}

// New takes the users from LoadUsers. Listing someone here lets them prove
// who they are; the allowlists still decide whether they get in.
func New(users map[string]string) (*Method, error) {
	if len(users) == 0 {
		return nil, fmt.Errorf("no password users")
	}
	return &Method{
		users: users, decoy: Hash(rand.Text()), slots: make(chan struct{}, 4),
		// A shared front proxy makes every client one address, so its limit
		// is looser and shorter than the per-account one.
		byEmail: newLimiter(5, 5*time.Minute), byClient: newLimiter(30, time.Minute),
	}, nil
}

func (m *Method) ID() string { return methodID }

func (m *Method) Mount(mux *http.ServeMux, host auth.Host) {
	m.host = host
	mux.HandleFunc("POST "+postPath, m.submit)
}

type flowData struct {
	Token string `json:"t"`
}

var formTmpl = template.Must(template.New("form").Parse(`<form method="post" action="` + postPath + `">
<input type="hidden" name="csrf" value="{{.Token}}"><input type="hidden" name="next" value="{{.Next}}">
<label>E-mail<input type="email" name="email" value="{{.Email}}" autocomplete="username" required maxlength="254"{{if not .Email}} autofocus{{end}}></label>
<label>Password<input type="password" name="password" autocomplete="current-password" required{{if .Email}} autofocus{{end}}></label>
<button type="submit">Sign in</button>
</form>`))

// Form ties a token to the flow cookie: a cross-site form post carries no
// SameSite=Lax cookie, so it cannot sign a victim into someone else's account.
func (m *Method) Form(w http.ResponseWriter, r *http.Request, next string) template.HTML {
	tok := rand.Text()
	if err := m.host.SaveFlow(w, methodID, flowData{Token: tok}); err != nil {
		return ""
	}
	var b bytes.Buffer
	// After a failed attempt, keep the e-mail so only the password is retyped.
	var email string
	if r.Method == http.MethodPost && len(r.PostForm.Get("email")) <= 254 {
		email = r.PostForm.Get("email")
	}
	if err := formTmpl.Execute(&b, map[string]string{"Token": tok, "Next": next, "Email": email}); err != nil {
		return ""
	}
	return template.HTML(b.String())
}

func (m *Method) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8*maxField)
	if err := r.ParseForm(); err != nil {
		m.host.Fail(w, http.StatusBadRequest, "Bad sign-in request.")
		return
	}
	next := r.PostForm.Get("next")
	var f flowData
	tok := r.PostForm.Get("csrf")
	if !m.host.TakeFlow(w, r, methodID, &f) || tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(f.Token)) != 1 {
		m.host.LoginPage(w, r, http.StatusBadRequest, next, "The sign-in form expired. Try again.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostForm.Get("email")))
	pw := r.PostForm.Get("password")
	if email == "" || pw == "" || len(email) > maxField || len(pw) > maxField {
		m.host.LoginPage(w, r, http.StatusBadRequest, next, "Enter your e-mail and password.")
		return
	}
	client := clientAddr(r)
	if d := max(m.byEmail.wait(email), m.byClient.wait(client)); d > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(d.Seconds())+1))
		m.host.LoginPage(w, r, http.StatusTooManyRequests, next,
			fmt.Sprintf("Too many failed attempts. Try again in %s.", d.Round(time.Second)+time.Second))
		return
	}
	ok, err := m.verify(r.Context(), email, pw)
	if err != nil {
		return // the client went away while waiting for a slot
	}
	if !ok {
		m.byEmail.fail(email)
		m.byClient.fail(client)
		log.Warn().Str("email", email).Str("remote", client).Msg("password sign-in failed")
		m.host.LoginPage(w, r, http.StatusUnauthorized, next, "Wrong e-mail or password.")
		return
	}
	m.byEmail.reset(email)
	m.host.SignIn(w, r, methodID, auth.Identity{Email: email}, next)
}

func (m *Method) verify(ctx context.Context, email, pw string) (bool, error) {
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	h, known := m.users[email]
	if !known {
		h = m.decoy
	}
	return Verify(h, pw) && known, nil
}

func clientAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
