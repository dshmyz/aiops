package assistant_test

import (
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

func viewer() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:            "viewer-1",
		Roles:              []string{"viewer"},
		AllowedEnvironments: []string{"prod"},
		RequestID:          "request-viewer",
	}
}

func user() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:            "admin-1",
		Roles:              []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
		RequestID:          "request-admin",
	}
}

func admin() identity.CurrentUser {
	return user()
}
