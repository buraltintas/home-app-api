package user

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestValidateUpdateRejectsUnsafeOrOversizedProfileData(t *testing.T) {
	bad := []Update{
		{AvatarURL: ptr("javascript:alert(1)")},
		{Bio: ptr(strings.Repeat("x", 501))},
		{HousingStatus: ptr("spaceship")},
		{HomeStyleInterests: ptr([]string{""})},
	}
	for i := range bad {
		if validateUpdate(&bad[i]) == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func TestValidateUpdateNormalizesProfileData(t *testing.T) {
	in := Update{Username: ptr("  valid_user "), DisplayName: ptr("  Ayşe  "), AvatarURL: ptr("https://cdn.example/avatar.jpg"), HomeStyleInterests: ptr([]string{" modern "})}
	if e := validateUpdate(&in); e != nil {
		t.Fatal(e)
	}
	if *in.Username != "valid_user" || *in.DisplayName != "Ayşe" || (*in.HomeStyleInterests)[0] != "modern" {
		t.Fatalf("not normalized: %+v", in)
	}
}

func TestValidateUpdateCountsUnicodeCharactersNotBytes(t *testing.T) {
	accepted := Update{Bio: ptr(strings.Repeat("ä", 500))}
	if err := validateUpdate(&accepted); err != nil {
		t.Fatalf("500 Unicode characters rejected: %v", err)
	}
	rejected := Update{Bio: ptr(strings.Repeat("ä", 501))}
	if err := validateUpdate(&rejected); err == nil {
		t.Fatal("501 Unicode characters accepted")
	}
}
