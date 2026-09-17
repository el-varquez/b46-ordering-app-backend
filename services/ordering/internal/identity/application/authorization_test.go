package application

import (
	"errors"
	"testing"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

func TestAuthorizeUsesExactRoles(t *testing.T) {
	roles := []domain.Role{domain.RoleCustomer, domain.RoleCashier, domain.RoleAdmin}
	for _, actual := range roles {
		for _, required := range roles {
			name := string(actual) + "_to_" + string(required)
			t.Run(name, func(t *testing.T) {
				err := Authorize(domain.Principal{Role: actual, Status: domain.AccountActive}, required)
				if actual == required && err != nil {
					t.Fatalf("Authorize() error = %v, want allow", err)
				}
				if actual != required && !errors.Is(err, domain.ErrForbidden) {
					t.Fatalf("Authorize() error = %v, want ErrForbidden", err)
				}
			})
		}
	}
}

func TestAuthorizeRejectsDisabledPrincipal(t *testing.T) {
	err := Authorize(domain.Principal{
		Role: domain.RoleAdmin, Status: domain.AccountDisabled,
	}, domain.RoleAdmin)
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Authorize() error = %v, want ErrUnauthenticated", err)
	}
}
