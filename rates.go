package main

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
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

func currencyCode(currency Currency) string {
	return strings.ToUpper(string(currency))
}

type RatesClient interface {
	fetchRates() (map[Currency]float64, error)
}

type CoinGeckoClient struct {
	currencies string
}

func newCoinGeckoClient() *CoinGeckoClient {
	var currencies strings.Builder
	for _, currency := range supportedCurrencies() {
		currencies.WriteString("," + string(currency))
	}

	return &CoinGeckoClient{currencies: currencies.String()[1:]}
}

func (client *CoinGeckoClient) fetchRates() (map[Currency]float64, error) {
	url := "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=" + client.currencies
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("User-Agent", "lnurld/1.0")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()
	bodyBytes, _ := io.ReadAll(response.Body)

	var ratesResponse struct {
		Bitcoin map[Currency]float64 `json:"bitcoin"`
	}
	if err := json.Unmarshal(bodyBytes, &ratesResponse); err != nil {
		return nil, err
	}

	return ratesResponse.Bitcoin, nil
}

type RatesService struct {
	ratesClient RatesClient
	rates       atomic.Pointer[map[Currency]float64]
}

func newRatesService(ratesClient RatesClient, refreshPeriod time.Duration) *RatesService {
	service := RatesService{ratesClient: ratesClient}
	if err := service.refreshRates(); err != nil {
		log.Fatalln("error fetching rates:", err)
	}

	go func() {
		for true {
			time.Sleep(refreshPeriod)
			err := service.refreshRates()
			if err != nil {
				log.Println("error updating rates:", err)
			}
		}
	}()

	return &service
}

func (service *RatesService) refreshRates() error {
	rates, err := service.ratesClient.fetchRates()
	if err != nil {
		return err
	}

	service.rates.Store(&rates)

	return nil
}

func (service *RatesService) getExchangeRates() map[Currency]float64 {
	exchangeRates := map[Currency]float64{}
	for currency, exchangeRate := range *service.rates.Load() {
		exchangeRates[currency] = exchangeRate / satsPerBitcoin
	}

	return exchangeRates
}

func (service *RatesService) getExchangeRate(currency Currency) float64 {
	return (*service.rates.Load())[currency]
}

func (service *RatesService) fiatToSats(currency Currency, amount float64) uint32 {
	exchangeRate := service.getExchangeRate(currency)
	sats := math.Round(satsPerBitcoin / exchangeRate * amount)

	return uint32(sats)
}

func (service *RatesService) satsToFiat(currency Currency, sats int64) float64 {
	exchangeRate := service.getExchangeRate(currency)
	amount := float64(sats) * exchangeRate / satsPerBitcoin

	return amount
}
