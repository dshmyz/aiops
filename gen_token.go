package main

import (
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"os"
	"time"
)

func main() {
	secret := []byte(os.Getenv("COPILOT_JWT_HMAC_SECRET"))
	if len(secret) == 0 {
		fmt.Fprintln(os.Stderr, "COPILOT_JWT_HMAC_SECRET is not set")
		os.Exit(1)
	}
	claims := jwt.MapClaims{
		"sub":   "admin-1",
		"roles": []string{"admin"},
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString(secret)
	fmt.Println(s)
}
