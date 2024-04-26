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

type AuthenticationConfig struct {
	RequestExpiry time.Duration `yaml:"request-expiry"`
}

type AuthenticationService struct {
	k1s *expirable.LRU[string, string]
}

func newAuthenticationService(config AuthenticationConfig) *AuthenticationService {
	requestExpiry := config.RequestExpiry
	if requestExpiry < 1*time.Minute || requestExpiry > 10*time.Minute {
		log.Fatal("Authentication request expiry out of range: ", requestExpiry)
	}

	return &AuthenticationService{
		k1s: expirable.NewLRU[string, string](1024, nil, requestExpiry),
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

	service.k1s.Add(k1, key)

	return nil
}

func (service *AuthenticationService) identity(k1 string) string {
	if storedKey, k1Valid := service.k1s.Get(k1); k1Valid {
		return storedKey
	}
	return ""
}

func toIdentityId(identity string) string {
	if identityBytes, _ := hex.DecodeString(identity); len(identityBytes) >= 7 {
		return base58.Encode(identityBytes)[0:7]
	}
	return ""
}
