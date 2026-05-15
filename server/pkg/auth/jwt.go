package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID int64 `json:"sub,string"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret    []byte
	accessTTL time.Duration
}

func NewIssuer(secret string, accessTTLHours int) *Issuer {
	return &Issuer{
		secret:    []byte(secret),
		accessTTL: time.Duration(accessTTLHours) * time.Hour,
	}
}

func (i *Issuer) IssueAccess(userID int64) (string, time.Time, error) {
	exp := time.Now().Add(i.accessTTL)
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return s, exp, nil
}

func (i *Issuer) Parse(tokenStr string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
