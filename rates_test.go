package main

import (
	"sync"
	"testing"
)

type TestClient struct{}

func (client *TestClient) fetchRates() (map[Currency]float64, error) {
	return map[Currency]float64{CZK: 1_000_000, EUR: 40_000}, nil
}

func TestRatesService(t *testing.T) {
	service := newRatesService(&TestClient{}, 0)

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
