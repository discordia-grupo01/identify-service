package model

import "time"

type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Description  string    `json:"description"`
	AvatarURL    string    `json:"avatar_url"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RegisterRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type PasswordResetRequest struct {
	Token           string `json:"token" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

type AuthResponse struct {
	User  UserResponse `json:"user"`
	Token string       `json:"token"`
}

type UserResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Description string    `json:"description"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
}

type PublicUserResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url"`
	CreatedAt time.Time `json:"created_at"`
}

type ProfileUpdateRequest struct {
	Name             *string
	Description      *string
	Image            []byte
	ImageContentType string
	ImageProvided    bool
}

func NewUserResponse(user *User) *UserResponse {
	return &UserResponse{
		ID:          user.ID,
		Name:        user.Name,
		Email:       user.Email,
		Description: user.Description,
		AvatarURL:   user.AvatarURL,
		CreatedAt:   user.CreatedAt.UTC(),
	}
}

func NewPublicUserResponse(user *User) *PublicUserResponse {
	return &PublicUserResponse{
		ID:        user.ID,
		Name:      user.Name,
		AvatarURL: user.AvatarURL,
		CreatedAt: user.CreatedAt.UTC(),
	}
}
