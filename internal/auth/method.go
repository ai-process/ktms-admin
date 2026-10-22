package auth

import (
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// Identity is who a sign-in method proved the caller to be. The method must
// have verified the e-mail; roles still come from the allowlists.
type Identity struct {
	Email   string
	Name    string
	Picture string
}

// Method is one way to sign in. A method owns its routes, normally under
// /auth/<ID>/, and finishes a successful sign-in with Host.SignIn.
type Method interface {
	ID() string
	Mount(mux *http.ServeMux, host Host)
}

// Button is a method started from a link on the sign-in page, such as a
// redirect to an identity provider.
type Button interface {
	Method
	Label() string
	StartURL(next string) string
}

// Identifier is a method that identifies every request on its own, such as
// an access proxy in front of the console. It needs no session: when the
// proxy stops vouching for someone, their access ends with the next request.
type Identifier interface {
	Method
	Identify(r *http.Request) (Identity, bool)
}

// Host is what the core offers a sign-in method.
type Host interface {
	// SignIn applies the allowlists, starts a session and redirects to next.
	SignIn(w http.ResponseWriter, r *http.Request, method string, id Identity, next string)
	// Fail shows a plain-text error on the sign-in page.
	Fail(w http.ResponseWriter, code int, msg string)
	// SaveFlow keeps v in a short-lived signed cookie across a redirect.
	SaveFlow(w http.ResponseWriter, method string, v any) error
	// TakeFlow reads and clears it; false when missing, forged, expired or another method's.
	TakeFlow(w http.ResponseWriter, r *http.Request, method string, v any) bool
	PublicURL() string
}

type flowEnvelope struct {
	Method string          `json:"m"`
	Exp    int64           `json:"x"`
	Value  json.RawMessage `json:"v"`
}

func (h *Handler) PublicURL() string { return h.o.PublicURL }

func (h *Handler) SaveFlow(w http.ResponseWriter, method string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sealed, err := h.o.Signer.Seal(flowEnvelope{Method: method, Exp: time.Now().Add(flowTTL).Unix(), Value: raw})
	if err != nil {
		return err
	}
	h.setCookie(w, flowCookie, sealed, flowTTL)
	return nil
}

func (h *Handler) TakeFlow(w http.ResponseWriter, r *http.Request, method string, v any) bool {
	c, err := r.Cookie(flowCookie)
	if err != nil {
		return false
	}
	h.clearCookie(w, flowCookie)
	var f flowEnvelope
	if h.o.Signer.Open(c.Value, &f) != nil || f.Method != method || time.Now().Unix() >= f.Exp {
		return false
	}
	return json.Unmarshal(f.Value, v) == nil
}

func (h *Handler) SignIn(w http.ResponseWriter, r *http.Request, method string, id Identity, next string) {
	role := h.roleFor(id.Email)
	if role == "" {
		log.Warn().Str("email", id.Email).Str("method", method).Msg("sign-in refused: not on the allowlist")
		h.Fail(w, http.StatusForbidden, id.Email+" is not allowed to use this console.")
		return
	}
	sealed, err := h.o.Signer.Seal(sessionData{Email: id.Email, Name: id.Name, Picture: id.Picture, Method: method,
		Exp: time.Now().Add(h.o.SessionTTL).Unix()})
	if err != nil {
		h.Fail(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	h.setCookie(w, sessionCookie, sealed, h.o.SessionTTL)
	log.Info().Str("email", id.Email).Str("role", role).Str("method", method).Msg("signed in")
	http.Redirect(w, r, SafeNext(next), http.StatusFound)
}

func (h *Handler) Fail(w http.ResponseWriter, code int, msg string) {
	h.page(w, code, pageData{Error: msg})
}

// Mount registers the sign-in page, sign-out and every method's routes.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", h.Login)
	mux.HandleFunc("POST /auth/logout", h.Logout)
	for _, m := range h.o.Methods {
		m.Mount(mux, h)
	}
}

// Login shows the sign-in page, or goes straight to the only method there is.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	next := SafeNext(r.URL.Query().Get("next"))
	if h.o.InsecureNoAuth {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if _, ok := h.UserFor(r); ok {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	var d pageData
	for _, m := range h.o.Methods {
		switch m := m.(type) {
		case Button:
			d.Buttons = append(d.Buttons, pageButton{Label: m.Label(), URL: m.StartURL(next)})
		case Identifier:
			// The proxy vouched for someone the allowlists do not know.
			if id, ok := m.Identify(r); ok {
				h.Fail(w, http.StatusForbidden, id.Email+" is not allowed to use this console.")
				return
			}
			d.Proxy = true
		}
	}
	switch {
	case len(h.o.Methods) == 0:
		h.Fail(w, http.StatusInternalServerError, "No sign-in method is configured.")
	case len(h.o.Methods) == 1 && len(d.Buttons) == 1:
		http.Redirect(w, r, d.Buttons[0].URL, http.StatusFound)
	default:
		h.page(w, http.StatusOK, d)
	}
}

type pageButton struct{ Label, URL string }

type pageData struct {
	Error   string
	Buttons []pageButton
	// Proxy notes that an access proxy can sign you in too.
	Proxy bool
}

// The console's CSP forbids inline styles, so the page links /auth.css.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign in · KTMS admin</title><link rel="stylesheet" href="/auth.css"></head>
<body><main class="card"><h1>Sign in</h1>
{{- if .Error}}
<p class="error">{{.Error}}</p><p><a href="/auth/login">Try again</a></p>
{{- else}}
<p class="muted">KTMS admin console</p>
{{- range .Buttons}}
<a class="btn" href="{{.URL}}">{{.Label}}</a>
{{- end}}
{{- if .Proxy}}
<p class="note">{{if .Buttons}}Or open{{else}}Open{{end}} this console through your organisation's access proxy, which signs you in.</p>
{{- end}}
{{- end}}
</main></body></html>
`))

func (h *Handler) page(w http.ResponseWriter, code int, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := pageTmpl.Execute(w, d); err != nil {
		log.Error().Err(err).Msg("sign-in page")
	}
}
