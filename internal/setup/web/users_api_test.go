package web

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

func TestOwnerCount(t *testing.T) {
	users := user.UsersConfig{
		"alice": {Role: "owner"},
		"bob":   {Role: "user"},
		"carol": {Role: "owner"},
	}

	if got := ownerCount(users); got != 2 {
		t.Fatalf("ownerCount = %d, want 2", got)
	}
}
