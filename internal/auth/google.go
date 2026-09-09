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
	// The account picture, when Google sends one. It is the picture the person already
	// uses to sign in, so it is the obvious first avatar; it is only ever used to fill an
	// empty one, never to replace a picture they chose here.
	Picture string
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
	picture, _ := p.Claims["picture"].(string)
	if p.Subject == "" || strings.TrimSpace(email) == "" {
		return GoogleIdentity{}, errors.New("missing google identity claims")
	}
	// Anything but an https URL is not a picture we are willing to point a browser at.
	if picture = strings.TrimSpace(picture); !strings.HasPrefix(picture, "https://") {
		picture = ""
	}
	return GoogleIdentity{p.Subject, email, verified, picture}, nil
}
