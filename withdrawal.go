package main

import (
	"github.com/fiatjaf/go-lnurl"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"log"
	"time"
)

type WithdrawalConfig struct {
	FeePercent    float32       `yaml:"fee-percent"`
	RequestExpiry time.Duration `yaml:"request-expiry"`
}

type WithdrawalRequest struct {
	fileName    string
	amount      int64
	feeLimit    int64
	description string
}

type WithdrawalService struct {
	k1s        *expirable.LRU[string, *WithdrawalRequest]
	feePercent float32
}

func newWithdrawalService(config WithdrawalConfig) *WithdrawalService {
	feePercent, requestExpiry := config.FeePercent, config.RequestExpiry
	if feePercent < 0 || feePercent > 10 {
		log.Fatal("Withdrawal fee percent out of range: ", feePercent)
	}
	if requestExpiry < 1*time.Minute || requestExpiry > 10*time.Minute {
		log.Fatal("Withdrawal request expiry out of range: ", requestExpiry)
	}

	return &WithdrawalService{
		k1s:        expirable.NewLRU[string, *WithdrawalRequest](32, nil, requestExpiry),
		feePercent: feePercent,
	}
}

func (service *WithdrawalService) createRequest(fileName string, amount int64, description string) string {
	k1 := lnurl.RandomK1()
	fee := withdrawalFee(amount, service.feePercent)
	service.k1s.Add(k1, &WithdrawalRequest{
		fileName:    fileName,
		amount:      amount - fee,
		feeLimit:    fee,
		description: description,
	})

	return k1
}

func (service *WithdrawalService) getRequest(k1 string) *WithdrawalRequest {
	if request, k1Valid := service.k1s.Get(k1); k1Valid {
		return request
	}
	return nil
}

func (service *WithdrawalService) removeRequest(k1 string) {
	service.k1s.Remove(k1)
}

func withdrawalFee(amount int64, feePercent float32) int64 {
	return int64(float32(amount) * feePercent / 100)
}
