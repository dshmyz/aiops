package assistant_test

import (
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

func user() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:   "admin-1",
		Roles:     []string{"admin"},
		RequestID: "request-admin",
	}
}
