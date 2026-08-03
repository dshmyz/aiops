package httpapi

import (
	"net/http"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// AuthMode determines which authentication mechanisms are active.
type AuthMode string

const (
	AuthModeJWT  AuthMode = "jwt"
	AuthModeCAS  AuthMode = "cas"
	AuthModeBoth AuthMode = "both"
)

// MultiAuthenticator chains multiple authentication strategies. In "both" mode
// it tries JWT first (Authorization header) then CAS session cookie. In single
// mode it only attempts the configured strategy.
type MultiAuthenticator struct {
	mode AuthMode
	jwt  *HMACAuthenticator
	cas  *CASAuthenticator
}

// NewMultiAuthenticator creates an authenticator that dispatches based on mode.
// jwt may be nil when mode is "cas"; cas may be nil when mode is "jwt".
func NewMultiAuthenticator(mode AuthMode, jwt *HMACAuthenticator, cas *CASAuthenticator) *MultiAuthenticator {
	return &MultiAuthenticator{mode: mode, jwt: jwt, cas: cas}
}

// Authenticate implements the Authenticator interface.
func (m *MultiAuthenticator) Authenticate(request *http.Request) (identity.CurrentUser, error) {
	switch m.mode {
	case AuthModeCAS:
		return m.authenticateCAS(request)
	case AuthModeBoth:
		// Try JWT first when Authorization header is present.
		if request.Header.Get("Authorization") != "" {
			user, err := m.authenticateJWT(request)
			if err == nil {
				return user, nil
			}
		}
		// Fall back to CAS session cookie.
		return m.authenticateCAS(request)
	default: // "jwt" or empty
		return m.authenticateJWT(request)
	}
}

// Mode returns the active authentication mode.
func (m *MultiAuthenticator) Mode() AuthMode {
	return m.mode
}

// CAS returns the CAS authenticator (may be nil in jwt-only mode).
func (m *MultiAuthenticator) CAS() *CASAuthenticator {
	return m.cas
}

func (m *MultiAuthenticator) authenticateJWT(request *http.Request) (identity.CurrentUser, error) {
	if m.jwt == nil {
		return identity.CurrentUser{}, errAuthNotConfigured
	}
	return m.jwt.Authenticate(request)
}

func (m *MultiAuthenticator) authenticateCAS(request *http.Request) (identity.CurrentUser, error) {
	if m.cas == nil {
		return identity.CurrentUser{}, errAuthNotConfigured
	}
	return m.cas.Authenticate(request)
}

type authError string

func (e authError) Error() string { return string(e) }

const errAuthNotConfigured = authError("authentication method not configured")
