package smtp

import (
	"context"
	"testing"
)

func TestSMTPConfigurationRequiresTLSOrLoopbackDevelopment(t *testing.T) {
	if _, err := New("mail.example.test", 587, "hello@example.test", "", "", false); err == nil {
		t.Fatal("remote SMTP without credentials was accepted")
	}
	if _, err := New("mail.example.test", 1025, "hello@example.test", "", "", true); err == nil {
		t.Fatal("insecure remote SMTP was accepted")
	}
	if _, err := New("localhost", 1025, "hello@example.test", "", "", true); err != nil {
		t.Fatalf("loopback SMTP development configuration rejected: %v", err)
	}
}

func TestSMTPRejectsHeaderInjectionBeforeConnecting(t *testing.T) {
	sender, err := New("localhost", 1025, "hello@example.test", "", "", true)
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	if err := sender.SendCode(context.Background(), "customer@example.test\r\nBcc: attacker@example.test", "123456"); err == nil {
		t.Fatal("injected recipient was accepted")
	}
}
