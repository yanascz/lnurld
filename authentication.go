package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/fiatjaf/go-lnurl"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/mr-tron/base58"
	"log"
	"time"
)

type Identity string

func toIdentity(value string) Identity {
	return Identity(value)
}

func (identity Identity) String() string {
	return string(identity)
}

func (identity Identity) PublicId() string {
	if bytes, _ := hex.DecodeString(string(identity)); len(bytes) >= 7 {
		return base58.Encode(bytes)[0:7]
	}
	return "unknown"
}

type AuthenticationConfig struct {
	RequestExpiry time.Duration `yaml:"request-expiry"`
}

type AuthenticationService struct {
	credentials map[UserKey]string
	tokens      map[string]UserKey
	k1s         *expirable.LRU[string, Identity]
}

func newAuthenticationService(credentials map[UserKey]string, config AuthenticationConfig) *AuthenticationService {
	if len(credentials) == 0 {
		log.Fatal("Authentication credentials missing")
	}

	requestExpiry := config.RequestExpiry
	if requestExpiry < 1*time.Minute || requestExpiry > 10*time.Minute {
		log.Fatal("Authentication request expiry out of range: ", requestExpiry)
	}

	tokens := map[string]UserKey{}
	for user, password := range credentials {
		tokens[accessToken(user, password)] = user
	}

	return &AuthenticationService{
		credentials: credentials,
		tokens:      tokens,
		k1s:         expirable.NewLRU[string, Identity](1024, nil, requestExpiry),
	}
}

func (service *AuthenticationService) verifyCredentials(user UserKey, password string) bool {
	userPassword, userExists := service.credentials[user]
	return userExists && password == userPassword
}

func (service *AuthenticationService) getUser(token string) UserKey {
	return service.tokens[token]
}

func (service *AuthenticationService) getToken(user UserKey) string {
	if password, userExists := service.credentials[user]; userExists {
		return accessToken(user, password)
	}
	return ""
}

func (service *AuthenticationService) generateChallenge() string {
	k1 := lnurl.RandomK1()
	service.k1s.Add(k1, "")

	return k1
}

func (service *AuthenticationService) verifyChallenge(k1 string, sig string, key string) error {
	identity, k1Valid := service.k1s.Get(k1)
	if !k1Valid || identity != "" {
		return errors.New("invalid k1")
	}

	signatureValid, err := lnurl.VerifySignature(k1, sig, key)
	if err != nil {
		return err
	} else if !signatureValid {
		return errors.New("invalid signature")
	}

	service.k1s.Add(k1, Identity(key))

	return nil
}

func (service *AuthenticationService) getIdentity(k1 string) Identity {
	if identity, k1Valid := service.k1s.Get(k1); k1Valid {
		return identity
	}
	return ""
}

func accessToken(user UserKey, password string) string {
	hash := sha256.Sum256([]byte(string(user) + ":" + password))
	return base64.StdEncoding.EncodeToString(hash[:])
}
