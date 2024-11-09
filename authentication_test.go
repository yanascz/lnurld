package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestIdentity(t *testing.T) {
	identity := toIdentity("02c3b844b8104f0c1b15c507774c9ba7fc609f58f343b9b149122e944dd20c9362")
	assert.Equal(t, "02c3b844b8104f0c1b15c507774c9ba7fc609f58f343b9b149122e944dd20c9362", identity.String())
	assert.Equal(t, "pdeDCJ5", identity.PublicId())
	assert.Equal(t, "unknown", Identity("invalid").PublicId())
}

func TestAuthenticationService(t *testing.T) {
	service := newAuthenticationService(
		map[UserKey]string{"satoshi": "4dm!nS3cr3t"},
		AuthenticationConfig{RequestExpiry: 1 * time.Minute},
	)

	t.Run("verifyCredentials", func(t *testing.T) {
		assert.True(t, service.verifyCredentials("satoshi", "4dm!nS3cr3t"))
		assert.False(t, service.verifyCredentials("satoshi", "S3cr3t"))
		assert.False(t, service.verifyCredentials("satoshi", ""))
		assert.False(t, service.verifyCredentials("csw", "4dm!nS3cr3t"))
		assert.False(t, service.verifyCredentials("", "4dm!nS3cr3t"))
		assert.False(t, service.verifyCredentials("", ""))
	})

	t.Run("getToken", func(t *testing.T) {
		assert.Equal(t, "S5a4FZNT7zUF2u5nKX+Ksozcy4QSO9umR9SiwfIWaxQ=", service.getToken("satoshi"))
		assert.Empty(t, service.getToken("csw"))
		assert.Empty(t, service.getToken(""))
	})

	t.Run("getUser", func(t *testing.T) {
		assert.Equal(t, UserKey("satoshi"), service.getUser("S5a4FZNT7zUF2u5nKX+Ksozcy4QSO9umR9SiwfIWaxQ="))
		assert.Empty(t, service.getUser("S5a4FZNT7zUF2u5nKX+Ksozcy4QSO9umR9SiwfIWaxQ+"))
		assert.Empty(t, service.getUser(""))
	})

	t.Run("generateChallenge", func(t *testing.T) {
		k1 := service.generateChallenge()
		assert.Regexp(t, "^[0-9a-f]{64}$", k1)
		assert.NotEqual(t, k1, service.generateChallenge())
		assert.Empty(t, service.getIdentity(k1))
	})

	t.Run("verifyChallenge", func(t *testing.T) {
		k1 := "e2af6254a8df433264fa23f67eb8188635d15ce883e8fc020989d5f82ae6f11e"
		sig := "304402203767faf494f110b139293d9bab3c50e07b3bf33c463d4aa767256cd09132dc5102205821f8efacdb5c595b92ada255876d9201e126e2f31a140d44561cc1f7e9e43d"
		key := "02c3b844b8104f0c1b15c507774c9ba7fc609f58f343b9b149122e944dd20c9362"
		service.k1s.Add(k1, "")
		assert.NoError(t, service.verifyChallenge(k1, sig, key))
		assert.Error(t, service.verifyChallenge("invalid", sig, key))
		assert.Error(t, service.verifyChallenge(k1, "invalid", key))
		assert.Error(t, service.verifyChallenge(k1, sig, "invalid"))
		assert.Equal(t, Identity(key), service.getIdentity(k1))
	})
}
