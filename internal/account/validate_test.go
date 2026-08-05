package account

import (
	"errors"
	"testing"
)

func TestNormalizeAccountIdentity(t *testing.T) {
	name, err := NormalizeName("  Personal  ")
	if err != nil || name != "Personal" {
		t.Fatalf("name = %q, %v", name, err)
	}
	email, err := NormalizeEmail("Owner@iCloud.COM")
	if err != nil || email != "owner@icloud.com" {
		t.Fatalf("email = %q, %v", email, err)
	}
	username, err := NormalizeIMAPUsername("", email)
	if err != nil || username != email {
		t.Fatalf("username = %q, %v", username, err)
	}
}

func TestNormalizeAccountIdentityRejectsInvalidValues(t *testing.T) {
	for _, test := range []func() error{
		func() error { _, err := NormalizeName("\n"); return err },
		func() error { _, err := NormalizeEmail("not-an-address"); return err },
		func() error { _, err := NormalizeIMAPUsername("\x00", "owner@icloud.com"); return err },
	} {
		if err := test(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("error = %v", err)
		}
	}
}
