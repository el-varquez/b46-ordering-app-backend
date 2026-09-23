package application

import (
	"context"
	"errors"
	"testing"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type registrationValidationMailer struct{}

func (registrationValidationMailer) Configured() bool { return true }
func (registrationValidationMailer) SendCode(context.Context, string, string) error {
	return nil
}

func TestBeginRegistrationIdentifiesInvalidField(t *testing.T) {
	service := &RegistrationService{mailer: registrationValidationMailer{}}
	for _, scenario := range []struct {
		name     string
		input    string
		email    string
		password string
		want     error
	}{
		{"name", " ", "customer@example.com", "abcdefgh", domain.ErrInvalidRegistrationName},
		{"email", "Customer", "customer@localhost", "abcdefgh", domain.ErrInvalidRegistrationEmail},
		{"password", "Customer", "customer@example.com", "short", domain.ErrInvalidRegistrationPassword},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := service.Begin(context.Background(), scenario.input, scenario.email, scenario.password, "127.0.0.1")
			if !errors.Is(err, scenario.want) {
				t.Fatalf("Begin() error = %v, want %v", err, scenario.want)
			}
		})
	}
}
