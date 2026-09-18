package main

import (
	"sync"
	"testing"
)

// Rates are replaced by a background goroutine while request handlers read
// them, so every access has to go through the lock.
func TestRatesServiceConcurrentAccess(t *testing.T) {
	service := RatesService{rates: map[Currency]float64{CZK: 1_000_000, EUR: 40_000}}

	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for i := 0; i < 1000; i++ {
			service.setRates(map[Currency]float64{CZK: float64(1_000_000 + i), EUR: float64(40_000 + i)})
		}
	}()

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
}
