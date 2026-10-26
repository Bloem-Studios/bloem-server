package executor

import (
	"testing"
)

func TestBloemScenarioAccountPrerequisites(t *testing.T) {
	e := New(t)
	if !e.HasDatabase() {
		t.Fatal(DatabaseEnv + " is required for account fixture regression coverage")
	}
	u := e.users[fixtureMember]
	for _, token := range []string{e.accessToken(fixtureMember), e.refreshToken(fixtureMember)} {
		claims, err := e.jwt.ValidateToken(token)
		if err != nil {
			t.Fatal(err)
		}
		if claims.UserID != u.ID || claims.SessionID != e.sessions[fixtureMember] || claims.AccountIncarnationID != u.AccountIncarnationID.String() || claims.AuthMethod != "account" {
			t.Fatal("fixture token is not bound to its account and existing session")
		}
	}
}
