// Package identity projects verified identity claims into the request identity
// used by authorization. It deliberately has no permissions field: tool
// permissions are owned by the policy package, not by JWTs or clients.
package identity

import (
	"errors"
	"strings"
)

// TrustedClaims are claims obtained only after JWT verification or from a
// trusted gateway header. Callers must not populate them from request bodies.
type TrustedClaims struct {
	Subject string
	Roles   []string
}

// CurrentUser is the immutable identity projection used for one request.
type CurrentUser struct {
	Subject   string
	Roles     []string
	RequestID string
}

// Project validates and copies trusted identity claims. It never accepts or
// derives permissions; those remain a server-side policy decision.
func Project(claims TrustedClaims, requestID string) (CurrentUser, error) {
	if strings.TrimSpace(claims.Subject) == "" {
		return CurrentUser{}, errors.New("subject is required")
	}
	if strings.TrimSpace(requestID) == "" {
		return CurrentUser{}, errors.New("request ID is required")
	}

	roles, err := uniqueNonEmpty(claims.Roles, "roles")
	if err != nil {
		return CurrentUser{}, err
	}

	return CurrentUser{
		Subject:   strings.TrimSpace(claims.Subject),
		Roles:     roles,
		RequestID: strings.TrimSpace(requestID),
	}, nil
}

func uniqueNonEmpty(values []string, field string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New(field + " is required")
	}

	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New(field + " must not contain an empty value")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
