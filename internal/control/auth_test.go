package control

import (
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	h := hashPass("hunter2!!")
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("expected bcrypt hash, got %q", h)
	}
	if !checkPass("hunter2!!", h) {
		t.Error("correct password should verify")
	}
	if checkPass("wrong", h) {
		t.Error("wrong password should not verify")
	}
	if isLegacyHash(h) {
		t.Error("bcrypt hash should not be flagged legacy")
	}
}

func TestLegacyHashMigrationPath(t *testing.T) {
	lh := legacyHash("oldpass")
	if !isLegacyHash(lh) {
		t.Error("sha256 hash should be flagged legacy")
	}
	if !checkPass("oldpass", lh) {
		t.Error("legacy password should still verify against sha256 hash")
	}
	if checkPass("nope", lh) {
		t.Error("wrong legacy password should not verify")
	}
}
