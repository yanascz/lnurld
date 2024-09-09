package main

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"time"
)

const satsPerBitcoin = 100_000_000

type Currency string

const (
	CAD Currency = "cad"
	CHF Currency = "chf"
	CZK Currency = "czk"
	EUR Currency = "eur"
	GBP Currency = "gbp"
	USD Currency = "usd"
)

func supportedCurrencies() []Currency {
	return []Currency{CAD, CHF, CZK, EUR, GBP, USD}
}

type RatesService struct {
	currencies string
	rates      map[Currency]float64
}

func newRatesService(refreshPeriod time.Duration) *RatesService {
	var currencies string
	for _, currency := range supportedCurrencies() {
		currencies += "," + string(currency)
	}

	service := RatesService{currencies: currencies[1:]}
	if err := service.fetchRates(); err != nil {
		log.Fatalln("error fetching rates:", err)
	}

	go func() {
		for true {
			time.Sleep(refreshPeriod)
			err := service.fetchRates()
			if err != nil {
				log.Println("error updating rates:", err)
			}
		}
	}()

	return &service
}

func (service *RatesService) fetchRates() error {
	url := "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=" + service.currencies
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	request.Header.Set("User-Agent", "lnurld/1.0")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()
	bodyBytes, _ := io.ReadAll(response.Body)

	var ratesResponse struct {
		Bitcoin map[Currency]float64 `json:"bitcoin"`
	}
	if err := json.Unmarshal(bodyBytes, &ratesResponse); err != nil {
		return err
	}

	service.rates = ratesResponse.Bitcoin
	return nil
}

func (service *RatesService) getExchangeRates() map[Currency]float64 {
	exchangeRates := map[Currency]float64{}
	for currency, exchangeRate := range service.rates {
		exchangeRates[currency] = exchangeRate / satsPerBitcoin
	}

	return exchangeRates
}

func (service *RatesService) fiatToSats(currency Currency, amount float64) uint32 {
	exchangeRate := service.rates[currency]
	sats := math.Round(satsPerBitcoin / exchangeRate * amount)

	return uint32(sats)
}

func (service *RatesService) satsToFiat(currency Currency, sats int64) float64 {
	exchangeRate := service.rates[currency]
	amount := float64(sats) * exchangeRate / satsPerBitcoin

	return amount
}
