package main

import (
	"encoding/hex"
	"github.com/mr-tron/base58"
	"sort"
)

type Raffle struct {
	Id           string        `json:"-"`
	Owner        string        `json:"owner"`
	IsMine       bool          `json:"-"`
	Title        string        `json:"title" binding:"min=1,max=50"`
	TicketPrice  uint32        `json:"ticketPrice" binding:"min=1,max=1000000"`
	FiatCurrency Currency      `json:"fiatCurrency" binding:"required"`
	Prizes       []RafflePrize `json:"prizes" binding:"min=1,max=21"`
}

func (raffle *Raffle) GetPrizes() []string {
	var prizes []string
	for _, prize := range raffle.Prizes {
		for i := uint8(0); i < prize.Quantity; i++ {
			prizes = append(prizes, prize.Name)
		}
	}
	return prizes
}

func (raffle *Raffle) GetPrizesCount() int {
	var prizesCount int
	for _, prize := range raffle.Prizes {
		prizesCount += int(prize.Quantity)
	}
	return prizesCount
}

type RafflePrize struct {
	Name     string `json:"name" binding:"min=1,max=50"`
	Quantity uint8  `json:"quantity" binding:"min=1,max=10"`
}

type RaffleTicket struct {
	Number      string `json:"number"`
	PaymentHash string `json:"paymentHash"`
}

func raffleTicketNumber(paymentHash string) string {
	if bytes, err := hex.DecodeString(paymentHash); err == nil {
		return base58.Encode(bytes)[0:5]
	}
	return ""
}

func sortRaffles(raffles []*Raffle) []*Raffle {
	sort.Slice(raffles, func(i, j int) bool {
		raffleI, raffleJ := raffles[i], raffles[j]
		if raffleI.IsMine == raffleJ.IsMine {
			return raffleI.Title < raffleJ.Title
		}
		return raffleI.IsMine
	})
	return raffles
}
