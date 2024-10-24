package main

import (
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
	k1s *expirable.LRU[string, Identity]
}

func newAuthenticationService(config AuthenticationConfig) *AuthenticationService {
	requestExpiry := config.RequestExpiry
	if requestExpiry < 1*time.Minute || requestExpiry > 10*time.Minute {
		log.Fatal("Authentication request expiry out of range: ", requestExpiry)
	}

	return &AuthenticationService{
		k1s: expirable.NewLRU[string, Identity](1024, nil, requestExpiry),
	}
}

func (service *AuthenticationService) init() string {
	k1 := lnurl.RandomK1()
	service.k1s.Add(k1, "")

	return k1
}

func (service *AuthenticationService) verify(k1 string, sig string, key string) error {
	storedKey, k1Valid := service.k1s.Get(k1)
	if !k1Valid || storedKey != "" {
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

func (service *AuthenticationService) identity(k1 string) Identity {
	if storedKey, k1Valid := service.k1s.Get(k1); k1Valid {
		return storedKey
	}
	return ""
}
