package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// CASConfig holds configuration for CAS 3.0 SSO authentication.
type CASConfig struct {
	// ServerURL is the base URL of the CAS server (e.g. https://cas.example.com/cas).
	ServerURL string
	// ServiceURL is the public URL of this service that CAS redirects back to
	// (e.g. https://copilot.example.com). It is used as the "service" parameter
	// in CAS protocol messages.
	ServiceURL string
	// SessionSecret signs the local session cookie issued after successful CAS
	// validation. Must be non-empty.
	SessionSecret []byte
	// SessionTTL controls how long a CAS session cookie remains valid.
	// Defaults to 8 hours when zero.
	SessionTTL time.Duration
	// DefaultRoles are assigned to CAS users whose CAS attributes do not
	// include a "roles" attribute. Defaults to ["operator"].
	DefaultRoles []string
	// DefaultEnvironments are the allowed environments for CAS users.
	// Defaults to ["prod", "staging", "dev"].
	DefaultEnvironments []string
	// HTTPClient is used for ticket validation calls. Defaults to http.DefaultClient.
	HTTPClient *http.Client
}

const (
	casSessionCookieName = "copilot_cas_session"
	casDefaultSessionTTL = 8 * time.Hour
)

// CASAuthenticator implements CAS 3.0 ticket validation and local session
// management. It satisfies the Authenticator interface: requests carrying a
// valid session cookie are authenticated without contacting the CAS server.
type CASAuthenticator struct {
	config        CASConfig
	aliasExpander AliasExpander
}

// NewCASAuthenticator creates a CAS authenticator. Returns an error if the
// configuration is incomplete.
func NewCASAuthenticator(config CASConfig) (*CASAuthenticator, error) {
	if strings.TrimSpace(config.ServerURL) == "" {
		return nil, errors.New("CAS server URL is required")
	}
	if strings.TrimSpace(config.ServiceURL) == "" {
		return nil, errors.New("CAS service URL is required")
	}
	if len(config.SessionSecret) == 0 {
		return nil, errors.New("CAS session secret is required")
	}
	config.ServerURL = strings.TrimRight(config.ServerURL, "/")
	config.ServiceURL = strings.TrimRight(config.ServiceURL, "/")
	if config.SessionTTL == 0 {
		config.SessionTTL = casDefaultSessionTTL
	}
	if len(config.DefaultRoles) == 0 {
		config.DefaultRoles = []string{"operator"}
	}
	if len(config.DefaultEnvironments) == 0 {
		config.DefaultEnvironments = []string{"prod", "staging", "dev"}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &CASAuthenticator{config: config}, nil
}

// WithAliasExpander wires an alias expander that expands canonical environment
// identifiers with their aliases during CAS authentication. A nil expander is
// a no-op.
func (c *CASAuthenticator) WithAliasExpander(expander AliasExpander) *CASAuthenticator {
	c.aliasExpander = expander
	return c
}

// Authenticate checks for a valid CAS session cookie. If absent or invalid,
// it returns an error. Callers that want CAS login redirect behaviour should
// use the Router-level CAS handling (which issues 302 to the CAS login page).
func (c *CASAuthenticator) Authenticate(request *http.Request) (identity.CurrentUser, error) {
	cookie, err := request.Cookie(casSessionCookieName)
	if err != nil || cookie.Value == "" {
		return identity.CurrentUser{}, errors.New("no CAS session")
	}
	claims, err := c.verifySessionCookie(cookie.Value)
	if err != nil {
		return identity.CurrentUser{}, err
	}
	requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = newRequestID()
	}
	envs := claims.AllowedEnvironments
	if c.aliasExpander != nil {
		envs = c.aliasExpander.Expand(request.Context(), envs)
	}
	return identity.Project(identity.TrustedClaims{
		Subject:             claims.Subject,
		Roles:               claims.Roles,
		AllowedEnvironments: envs,
	}, requestID)
}

// LoginURL returns the CAS login URL that the browser should be redirected to.
func (c *CASAuthenticator) LoginURL() string {
	params := url.Values{"service": {c.config.ServiceURL + "/v1/auth/cas/callback"}}
	return c.config.ServerURL + "/login?" + params.Encode()
}

// LogoutURL returns the CAS logout URL.
func (c *CASAuthenticator) LogoutURL() string {
	params := url.Values{"service": {c.config.ServiceURL}}
	return c.config.ServerURL + "/logout?" + params.Encode()
}

// ValidateTicket performs CAS 3.0 ticket validation against the CAS server's
// /p3/serviceValidate endpoint. On success it returns the authenticated user
// and a signed session cookie value.
func (c *CASAuthenticator) ValidateTicket(ticket string) (identity.CurrentUser, string, error) {
	validationURL := c.config.ServerURL + "/p3/serviceValidate?" + url.Values{
		"ticket":  {ticket},
		"service": {c.config.ServiceURL + "/v1/auth/cas/callback"},
	}.Encode()

	client := c.config.HTTPClient
	resp, err := client.Get(validationURL)
	if err != nil {
		return identity.CurrentUser{}, "", fmt.Errorf("CAS validation request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return identity.CurrentUser{}, "", fmt.Errorf("CAS server returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return identity.CurrentUser{}, "", fmt.Errorf("read CAS response: %w", err)
	}

	user, err := parseCASValidationResponse(body, c.config.DefaultRoles, c.config.DefaultEnvironments)
	if err != nil {
		return identity.CurrentUser{}, "", err
	}

	cookieValue, err := c.signSessionCookie(casSessionClaims{
		Subject:             user.Subject,
		Roles:               user.Roles,
		AllowedEnvironments: user.AllowedEnvironments,
		ExpiresAt:           time.Now().Add(c.config.SessionTTL).Unix(),
	})
	if err != nil {
		return identity.CurrentUser{}, "", err
	}

	requestID := newRequestID()
	projected, err := identity.Project(identity.TrustedClaims{
		Subject:             user.Subject,
		Roles:               user.Roles,
		AllowedEnvironments: user.AllowedEnvironments,
	}, requestID)
	if err != nil {
		return identity.CurrentUser{}, "", err
	}
	return projected, cookieValue, nil
}

// SessionCookie builds the http.Cookie for a validated CAS session.
func (c *CASAuthenticator) SessionCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     casSessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(c.config.ServiceURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(c.config.SessionTTL.Seconds()),
	}
}

// ClearSessionCookie returns a cookie that expires the CAS session.
func (c *CASAuthenticator) ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     casSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	}
}

// --- CAS 3.0 XML response parsing ---

// casServiceResponse is the top-level XML element from CAS /p3/serviceValidate.
type casServiceResponse struct {
	XMLName xml.Name `xml:"serviceResponse"`
	Success struct {
		User       string `xml:"user"`
		Attributes struct {
			Roles       string `xml:"roles"`
			Email       string `xml:"email"`
			DisplayName string `xml:"displayName"`
		} `xml:"attributes"`
	} `xml:"authenticationSuccess"`
	Failure struct {
		Code    string `xml:"code"`
		Message string `xml:",chardata"`
	} `xml:"authenticationFailure"`
}

// casUser is the intermediate parsed user before identity projection.
type casUser struct {
	Subject             string
	Roles               []string
	AllowedEnvironments []string
}

func parseCASValidationResponse(body []byte, defaultRoles, defaultEnvs []string) (casUser, error) {
	var resp casServiceResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return casUser{}, fmt.Errorf("parse CAS XML response: %w", err)
	}
	if resp.Failure.Code != "" {
		msg := strings.TrimSpace(resp.Failure.Message)
		return casUser{}, fmt.Errorf("CAS authentication failed: %s %s", resp.Failure.Code, msg)
	}
	username := strings.TrimSpace(resp.Success.User)
	if username == "" {
		return casUser{}, errors.New("CAS response missing username")
	}

	roles := defaultRoles
	if raw := strings.TrimSpace(resp.Success.Attributes.Roles); raw != "" {
		roles = splitComma(raw)
	}

	return casUser{
		Subject:             username,
		Roles:               roles,
		AllowedEnvironments: defaultEnvs,
	}, nil
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// --- Session cookie signing ---

type casSessionClaims struct {
	Subject             string   `json:"sub"`
	Roles               []string `json:"roles"`
	AllowedEnvironments []string `json:"envs"`
	ExpiresAt           int64    `json:"exp"`
}

func (c *CASAuthenticator) signSessionCookie(claims casSessionClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.config.SessionSecret)
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

func (c *CASAuthenticator) verifySessionCookie(cookie string) (casSessionClaims, error) {
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 {
		return casSessionClaims{}, errors.New("malformed CAS session cookie")
	}
	mac := hmac.New(sha256.New, c.config.SessionSecret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return casSessionClaims{}, errors.New("CAS session signature invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return casSessionClaims{}, errors.New("CAS session decode failed")
	}
	var claims casSessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return casSessionClaims{}, errors.New("CAS session unmarshal failed")
	}
	if time.Now().Unix() > claims.ExpiresAt {
		return casSessionClaims{}, errors.New("CAS session expired")
	}
	return claims, nil
}
