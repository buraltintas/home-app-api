package auth

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/api/idtoken"
)

type GoogleIdentity struct {
	Subject, Email string
	EmailVerified  bool
}
type GoogleVerifier interface {
	Verify(context.Context, string) (GoogleIdentity, error)
}
type googleVerifier struct{ audience string }

func NewGoogleVerifier(audience string) GoogleVerifier { return &googleVerifier{audience} }
func (v *googleVerifier) Verify(ctx context.Context, raw string) (GoogleIdentity, error) {
	if v.audience == "" {
		return GoogleIdentity{}, errors.New("google auth is not configured")
	}
	p, err := idtoken.Validate(ctx, raw, v.audience)
	if err != nil {
		return GoogleIdentity{}, err
	}
	email, _ := p.Claims["email"].(string)
	verified, _ := p.Claims["email_verified"].(bool)
	if p.Subject == "" || strings.TrimSpace(email) == "" {
		return GoogleIdentity{}, errors.New("missing google identity claims")
	}
	return GoogleIdentity{p.Subject, email, verified}, nil
}
