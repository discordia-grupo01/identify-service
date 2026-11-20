package model

import (
	"database/sql"
	"encoding/json"
	"testing"
)

func TestNewUserResponseHasPassword(t *testing.T) {
	cases := []struct {
		name         string
		passwordHash sql.NullString
		want         bool
	}{
		{"account with password", sql.NullString{String: "$2a$10$hash", Valid: true}, true},
		{"google-only account", sql.NullString{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := NewUserResponse(&User{ID: "550e8400-e29b-41d4-a716-446655440000", PasswordHash: tc.passwordHash})

			if response.HasPassword != tc.want {
				t.Fatalf("HasPassword = %v, want %v", response.HasPassword, tc.want)
			}
		})
	}
}

func TestPublicUserResponseDoesNotExposeHasPassword(t *testing.T) {
	user := &User{
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		PasswordHash: sql.NullString{String: "$2a$10$hash", Valid: true},
	}

	body, err := json.Marshal(NewPublicUserResponse(user))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := fields["has_password"]; ok {
		t.Fatalf("public profile must not include has_password: %s", body)
	}
}
