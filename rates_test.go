package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type TestClient struct {
	delta float64
}

func (client *TestClient) fetchRates() (map[Currency]float64, error) {
	usdRate := 51_208.50 + client.delta
	eurRate := 43_525.19 + client.delta
	client.delta += 21

	return map[Currency]float64{USD: usdRate, EUR: eurRate}, nil
}

func TestRatesService(t *testing.T) {
	service := newRatesService(&TestClient{}, 100*time.Millisecond)

	t.Run("refreshRates", func(t *testing.T) {
		time.Sleep(150 * time.Millisecond)
		assert.Equal(t, 51_229.50, service.getExchangeRate(USD))
		assert.Equal(t, 43_546.19, service.getExchangeRate(EUR))
		assert.Zero(t, service.getExchangeRate(CZK))
	})

	t.Run("getExchangeRates", func(t *testing.T) {
		exchangeRates := service.getExchangeRates()
		assert.InDelta(t, 0.0005122950, exchangeRates[USD], 0.00000001)
		assert.InDelta(t, 0.0004354619, exchangeRates[EUR], 0.00000001)
		assert.Zero(t, exchangeRates[CZK])
	})

	t.Run("fiatToSats", func(t *testing.T) {
		assert.Equal(t, uint32(1952), service.fiatToSats(USD, 1))
		assert.Equal(t, uint32(2296), service.fiatToSats(EUR, 1))
		assert.Zero(t, service.fiatToSats(CZK, 1))
	})

	t.Run("satsToFiat", func(t *testing.T) {
		assert.Equal(t, 10.7581950, service.satsToFiat(USD, 21_000))
		assert.Equal(t, 18.2893998, service.satsToFiat(EUR, 42_000))
		assert.Zero(t, service.satsToFiat(CZK, 21_000))
	})

	service = newRatesService(&TestClient{}, 0)

	t.Run("concurrency", func(t *testing.T) {
		var waitGroup sync.WaitGroup
		for i := 0; i < 4; i++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				for j := 0; j < 1000; j++ {
					service.getExchangeRates()
					service.satsToFiat(CZK, 21_000)
					service.fiatToSats(EUR, 4.2)
				}
			}()
		}
		waitGroup.Wait()
	})
}
