package config

import (
	"strings"
	"testing"
)

func TestValidateRejectsShortJWTSecret(t *testing.T) {
	c := &Config{JWTSecret: strings.Repeat("x", minJWTSecretLen-1)}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected error for %d-byte secret, got nil", minJWTSecretLen-1)
	}
}

func TestValidateAcceptsSufficientJWTSecret(t *testing.T) {
	c := &Config{JWTSecret: strings.Repeat("x", minJWTSecretLen)}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error for %d-byte secret: %v", minJWTSecretLen, err)
	}
}
