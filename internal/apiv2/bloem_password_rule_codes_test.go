package apiv2

import (
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// Bloem's storefront routes name a refused password's rule with the codes
// Silo's v2 API gives the same refusal (passwordRuleProblem, Silo #5356).
func TestBloemPasswordRuleCodesAreSilos(t *testing.T) {
	policy := auth.PasswordPolicy{MinimumLength: 12, RequireUppercase: true, RequireSpecial: true}
	for _, password := range []string{"short", strings.Repeat("x", 73), "lowercase-only-1", "Uppercase1234"} {
		refusal := policy.Check(password)
		silo, siloOK := passwordRuleProblem(refusal, "password")
		bloem, bloemOK := handlers.BloemPasswordRuleCode(refusal)
		if !siloOK || !bloemOK || len(silo.Errors) != 1 || silo.Errors[0].Code != bloem {
			t.Errorf("%q: Silo %+v (%v), Bloem %q (%v)", password, silo, siloOK, bloem, bloemOK)
		}
	}
	if _, ok := handlers.BloemPasswordRuleCode(auth.ErrNotFound); ok {
		t.Fatal("an error that is no password refusal named a rule")
	}
}
