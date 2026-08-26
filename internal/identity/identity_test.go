package identity

import "testing"

func TestProjectCopiesOnlyTrustedIdentityFields(t *testing.T) {
	claims := TrustedClaims{
		Subject: "user-123",
		Roles:   []string{"operator", "operator"},
	}

	user, err := Project(claims, "req-456")
	if err != nil {
		t.Fatalf("project identity: %v", err)
	}

	if user.Subject != "user-123" {
		t.Fatalf("subject = %q, want %q", user.Subject, "user-123")
	}
	if user.RequestID != "req-456" {
		t.Fatalf("request ID = %q, want %q", user.RequestID, "req-456")
	}
	if !sameStrings(user.Roles, []string{"operator"}) {
		t.Fatalf("roles = %v, want [operator]", user.Roles)
	}

	claims.Roles[0] = "admin"
	if !sameStrings(user.Roles, []string{"operator"}) {
		t.Fatalf("roles changed after claims mutation: %v", user.Roles)
	}
}

func TestProjectRejectsIncompleteTrustedClaims(t *testing.T) {
	_, err := Project(TrustedClaims{Subject: "user-123", Roles: []string{"viewer"}}, "req-456")
	if err != nil {
		t.Fatalf("Project rejected valid claims: %v", err)
	}
	_, err = Project(TrustedClaims{Roles: []string{"viewer"}}, "req-456")
	if err == nil {
		t.Fatal("Project accepted claims without subject")
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
