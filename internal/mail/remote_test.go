package mail

import (
	"errors"
	"testing"
)

func TestRemoteReferenceRoundTripAndValidation(t *testing.T) {
	want := RemoteReference{
		AccountID:   "acct_0198d704-6c00-7000-8000-000000000001",
		FolderID:    "fld_0198d704-6c00-7000-8000-000000000002",
		UIDValidity: 77,
		UID:         42,
	}
	encoded, err := EncodeRemoteReference(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) < 8 || encoded[:4] != "rem_" {
		t.Fatalf("encoded reference = %q", encoded)
	}
	got, err := DecodeRemoteReference(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.AccountID != want.AccountID || got.FolderID != want.FolderID || got.UIDValidity != want.UIDValidity || got.UID != want.UID {
		t.Fatalf("decoded reference = %+v", got)
	}

	for _, value := range []RemoteReference{
		{},
		{AccountID: want.AccountID, FolderID: want.FolderID, UID: 42},
		{AccountID: want.AccountID, FolderID: want.FolderID, UIDValidity: 77},
	} {
		if _, err := EncodeRemoteReference(value); !errors.Is(err, ErrInvalidRemoteReference) {
			t.Fatalf("EncodeRemoteReference(%+v) error = %v", value, err)
		}
	}
	for _, value := range []string{"", "rem_", "rem_not-base64", "cur_abc"} {
		if _, err := DecodeRemoteReference(value); !errors.Is(err, ErrInvalidRemoteReference) {
			t.Fatalf("DecodeRemoteReference(%q) error = %v", value, err)
		}
	}
}
