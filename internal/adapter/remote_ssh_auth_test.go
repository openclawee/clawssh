package adapter

import "testing"

func TestBuildAuthMethods_PasswordOnly(t *testing.T) {
	methods, err := buildAuthMethods("", "", "secret")
	if err != nil {
		t.Fatalf("expected password auth to work, got err=%v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 auth method, got %d", len(methods))
	}
}

func TestBuildAuthMethods_NoCredentials(t *testing.T) {
	_, err := buildAuthMethods("", "", "")
	if err == nil {
		t.Fatal("expected error when no credentials provided")
	}
}
