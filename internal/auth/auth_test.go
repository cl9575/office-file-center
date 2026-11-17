package auth

import "testing"

func TestValidatePasswordMinimum(t *testing.T) {
	if err := ValidatePassword("12345"); err == nil {
		t.Fatal("5-character password should be rejected")
	}
	if err := ValidatePassword("123456"); err != nil {
		t.Fatalf("6-character password should be accepted: %v", err)
	}
}
