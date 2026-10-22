package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	sessionCookie = "lpa_session"
	flowCookie    = "lpa_login"
	flowTTL       = 10 * time.Minute

	RoleAdmin    = "admin"
	RoleTeamLead = "teamlead"
	RoleMember   = "member"
	RoleViewer   = "viewer"
)

type TeamMemberInfo struct {
	TeamID string
	OrgID  string
	Role   string
}

// ParseTeamMemberships reads email:team_id[:teamlead|member[:org_id]] entries.
// A malformed entry is skipped and logged: a typo must never grant powers.
func ParseTeamMemberships(raw string) map[string]TeamMemberInfo {
	out := make(map[string]TeamMemberInfo)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fields := strings.Split(p, ":")
		if len(fields) < 2 {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(fields[0]))
		teamID := strings.TrimSpace(fields[1])
		role := RoleMember
		if len(fields) >= 3 && strings.TrimSpace(fields[2]) != "" {
			role = strings.ToLower(strings.TrimSpace(fields[2]))
		}
		if email == "" || teamID == "" || (role != RoleTeamLead && role != RoleMember) {
			log.Warn().Str("entry", p).Msg("TEAM_MEMBERSHIPS: entry skipped (want email:team_id[:teamlead|member[:org_id]])")
			continue
		}
		var orgID string
		if len(fields) >= 4 {
			orgID = strings.TrimSpace(fields[3])
		}
		out[email] = TeamMemberInfo{
			TeamID: teamID,
			OrgID:  orgID,
			Role:   role,
		}
	}
	return out
}

// User is the authenticated caller.
type User struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	Role    string `json:"role"`
	OrgID   string `json:"org_id,omitempty"`
	TeamID  string `json:"team_id,omitempty"`
	// Method is the sign-in method's ID.
	Method string `json:"method,omitempty"`
	// Proxied users are vouched for by an access proxy on every request, so
	// signing out of the console alone does nothing for them.
	Proxied bool `json:"proxied,omitempty"`
}

func (u *User) IsSuperAdmin() bool {
	return u.Role == RoleAdmin
}

func (u *User) IsTeamLead() bool {
	return u.Role == RoleTeamLead && u.TeamID != ""
}

func (u *User) IsViewer() bool { return u.Role == RoleViewer }

// InTeam reports a team-scoped user: they see their own team, nothing global.
func (u *User) InTeam() bool {
	return (u.Role == RoleTeamLead || u.Role == RoleMember) && u.TeamID != ""
}

// CanReadGlobal: config, usage, nodes and every tenant's keys.
func (u *User) CanReadGlobal() bool { return u.IsSuperAdmin() || u.IsViewer() }

type sessionData struct {
	Email   string `json:"e"`
	Name    string `json:"n"`
	Picture string `json:"p"`
	Method  string `json:"m,omitempty"`
	Exp     int64  `json:"x"`
}

type Options struct {
	PublicURL       string
	Methods         []Method
	Admins          *Allowlist
	Viewers         *Allowlist
	TeamMemberships map[string]TeamMemberInfo
	DevUserRole     string
	DevUserTeamID   string
	DevUserOrgID    string
	Signer          *Signer
	SessionTTL      time.Duration
	SecureCookies   bool
	InsecureNoAuth  bool
}

// Handler is the sign-in core: sessions, roles and the sign-in page. The
// methods behind it prove identities.
type Handler struct {
	o Options
}

func New(o Options) *Handler { return &Handler{o: o} }

// memberFor is evaluated on every request so list changes apply at once.
func (h *Handler) memberFor(email string) (role, teamID, orgID string) {
	email = strings.ToLower(strings.TrimSpace(email))
	// An explicit membership names one person, so it beats an allowlist
	// pattern such as *@example.com that would otherwise make them admin.
	if info, ok := h.o.TeamMemberships[email]; ok {
		return info.Role, info.TeamID, info.OrgID
	}
	if h.o.Admins != nil && h.o.Admins.Match(email) {
		return RoleAdmin, "", ""
	}
	if h.o.Viewers != nil && h.o.Viewers.Match(email) {
		return RoleViewer, "", ""
	}
	return "", "", ""
}

func (h *Handler) roleFor(email string) string {
	role, _, _ := h.memberFor(email)
	return role
}

// UserFor returns the signed-in user for a request, if any.
func (h *Handler) UserFor(r *http.Request) (*User, bool) {
	if h.o.InsecureNoAuth {
		role := h.o.DevUserRole
		if role == "" {
			role = RoleAdmin
		}
		return &User{
			Email:  "dev@localhost",
			Name:   "Local dev",
			Role:   role,
			TeamID: h.o.DevUserTeamID,
			OrgID:  h.o.DevUserOrgID,
		}, true
	}
	for _, m := range h.o.Methods {
		if m, ok := m.(Identifier); ok {
			if id, ok := m.Identify(r); ok {
				return h.user(id, m.ID(), true)
			}
		}
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	var s sessionData
	if err := h.o.Signer.Open(c.Value, &s); err != nil || time.Now().Unix() >= s.Exp || !h.hasMethod(s.Method) {
		return nil, false
	}
	return h.user(Identity{Email: s.Email, Name: s.Name, Picture: s.Picture}, s.Method, false)
}

func (h *Handler) user(id Identity, method string, proxied bool) (*User, bool) {
	role, teamID, orgID := h.memberFor(id.Email)
	if role == "" {
		return nil, false
	}
	return &User{
		Email:   id.Email,
		Name:    id.Name,
		Picture: id.Picture,
		Role:    role,
		TeamID:  teamID,
		OrgID:   orgID,
		Method:  method,
		Proxied: proxied,
	}, true
}

// hasMethod lets turning a method off end its sessions. Sessions from before
// methods were recorded came from OIDC, the only method then.
func (h *Handler) hasMethod(id string) bool {
	if id == "" {
		id = "oidc"
	}
	for _, m := range h.o.Methods {
		if m.ID() == id {
			return true
		}
	}
	return false
}

// SafeNext only lets same-site absolute paths through, never an open redirect.
func SafeNext(next string) string {
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

// Logout drops the session. POST only, so a link or image cannot sign you out.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.clearCookie(w, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}
