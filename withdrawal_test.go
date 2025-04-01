package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestWithdrawalService(t *testing.T) {
	service := newWithdrawalService(
		WithdrawalConfig{FeePercent: 0.21, RequestExpiry: 1 * time.Minute},
	)

	t.Run("createRequest", func(t *testing.T) {
		k1 := service.createRequest("foo.csv", 21_000, "Sats")
		request := WithdrawalRequest{fileName: "foo.csv", amount: 20_956, feeLimit: 44, description: "Sats"}
		assert.Regexp(t, "^[0-9a-f]{64}$", k1)
		assert.Equal(t, &request, service.getRequest(k1))
		assert.NotEqual(t, k1, service.createRequest("bar.csv", 21, ""))
	})

	t.Run("removeRequest", func(t *testing.T) {
		k1 := service.createRequest("bar.csv", 0, "")
		request := WithdrawalRequest{fileName: "bar.csv", amount: 0, feeLimit: 0}
		assert.Equal(t, &request, service.getRequest(k1))
		service.removeRequest(k1)
		assert.Nil(t, service.getRequest(k1))
	})
}
