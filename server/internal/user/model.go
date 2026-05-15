package user

import "time"

type User struct {
	ID            int64     `json:"id,string"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Nickname      string    `json:"nickname"`
	AvatarURL     string    `json:"avatarUrl,omitempty"`
	Bio           string    `json:"bio,omitempty"`
	Status        int16     `json:"status"`
	EmailVerified bool      `json:"emailVerified"`
	IsAdmin       bool      `json:"isAdmin"`
	CreatedAt     time.Time `json:"createdAt"`
}
