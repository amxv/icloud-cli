package mail

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestRemoteSearchCursorV2RoundTripAndValidation(t *testing.T) {
	want := RemoteSearchCursor{
		Version: 2, AccountID: "acct_test", QueryHash: strings.Repeat("a", 64), Limit: 20,
		AllFolders: true, FolderSignature: strings.Repeat("b", 64), FolderCount: 2, InventoryComplete: true,
		Folders: []RemoteSearchFolderCursor{
			{FolderID: "fld_a", UIDValidity: 11, Head: &RemoteSearchCursorHead{UID: 90, InternalDate: time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)}},
			{FolderID: "fld_b", UIDValidity: 12, NextUID: 40, PendingUIDs: []uint32{60, 50}},
		},
	}
	encoded, err := EncodeRemoteSearchCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRemoteSearchCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || !got.AllFolders || got.FolderCount != 2 || !got.InventoryComplete || len(got.Folders) != 2 || got.Folders[0].Head == nil || got.Folders[0].Head.UID != 90 || got.Folders[1].NextUID != 40 || len(got.Folders[1].PendingUIDs) != 2 {
		t.Fatalf("decoded cursor = %+v", got)
	}

	invalid := []RemoteSearchCursor{
		{Version: 2, AccountID: "acct_test", QueryHash: strings.Repeat("a", 64), Limit: 20, AllFolders: true, FolderSignature: strings.Repeat("b", 64), FolderCount: 2},
		{Version: 2, AccountID: "acct_test", QueryHash: strings.Repeat("a", 64), Limit: 20, AllFolders: true, FolderSignature: strings.Repeat("b", 64), FolderCount: 2, InventoryComplete: true, Folders: []RemoteSearchFolderCursor{{FolderID: "fld_b", UIDValidity: 1, Done: true}, {FolderID: "fld_a", UIDValidity: 1, Done: true}}},
		{Version: 2, AccountID: "acct_test", QueryHash: strings.Repeat("a", 64), Limit: 20, AllFolders: true, FolderSignature: strings.Repeat("b", 64), FolderCount: 1, InventoryComplete: true, Folders: []RemoteSearchFolderCursor{{FolderID: "fld_a", UIDValidity: 1}}},
	}
	for _, value := range invalid {
		if _, err := EncodeRemoteSearchCursor(value); !errors.Is(err, ErrInvalidRemoteSearchCursor) {
			t.Fatalf("EncodeRemoteSearchCursor(%+v) error = %v", value, err)
		}
	}
}
