package folder

import "testing"

func TestValidateAdminNameAndProtectedRole(t *testing.T) {
	for _, value := range []string{"", " INBOX", "INBOX", "/Projects", "Projects/", "Projects//Broken", "Projects\nBroken"} {
		if err := ValidateAdminName(value, "/"); err == nil {
			t.Fatalf("expected invalid/protected name %q", value)
		}
	}
	if err := ValidateAdminName("Projects/日本語", "/"); err != nil {
		t.Fatalf("valid nested unicode name: %v", err)
	}
	if !ProtectedFromDestructiveMutation(Folder{Name: "Localized Sent", Kind: KindSent}) || !ProtectedFromDestructiveMutation(Folder{Name: "INBOX", Kind: KindOther}) {
		t.Fatal("system roles must be protected")
	}
	if ProtectedFromDestructiveMutation(Folder{Name: "Archive", Kind: KindOther}) {
		t.Fatal("ordinary English folder name must not be treated as a protected role")
	}
}
