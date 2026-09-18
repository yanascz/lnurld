package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// A currency missing from the rates response divides by zero. Converting the
// resulting infinity to uint32 is implementation-defined, so pin the result
// down rather than relying on what the current architecture happens to do.
func TestFiatToSatsWithoutExchangeRate(t *testing.T) {
	service := RatesService{rates: map[Currency]float64{EUR: 40_000}}

	assert.Equal(t, uint32(10_500), service.fiatToSats(EUR, 4.2))
	assert.Equal(t, uint32(0), service.fiatToSats(CZK, 4.2))
	assert.Equal(t, float64(0), service.satsToFiat(CZK, 21_000))
}
