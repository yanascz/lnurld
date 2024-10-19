package main

import (
	"github.com/mr-tron/base58"
	"sort"
)

type RaffleId string

type Raffle struct {
	Id           RaffleId      `json:"-"`
	Owner        UserKey       `json:"owner"`
	IsMine       bool          `json:"-"`
	Title        string        `json:"title" binding:"min=1,max=50"`
	TicketPrice  uint32        `json:"ticketPrice" binding:"min=1,max=1000000"`
	FiatCurrency Currency      `json:"fiatCurrency" binding:"required"`
	Prizes       []RafflePrize `json:"prizes" binding:"min=1,max=21"`
}

func (raffle *Raffle) PrizesCount() int {
	var prizesCount int
	for _, prize := range raffle.Prizes {
		prizesCount += int(prize.Quantity)
	}
	return prizesCount
}

func (raffle *Raffle) prizes() []string {
	var prizes []string
	for _, prize := range raffle.Prizes {
		for i := uint8(0); i < prize.Quantity; i++ {
			prizes = append(prizes, prize.Name)
		}
	}
	return prizes
}

type RafflePrize struct {
	Name     string `json:"name" binding:"min=1,max=50"`
	Quantity uint8  `json:"quantity" binding:"min=1,max=10"`
}

type RaffleTicket string

func toRaffleTicket(value string) RaffleTicket {
	return RaffleTicket(value)
}

func (ticket RaffleTicket) number() string {
	return base58.Encode(PaymentHash(ticket).bytes())[0:5]
}

func (ticket RaffleTicket) paymentHash() PaymentHash {
	return PaymentHash(ticket)
}

type RaffleDrawTicket struct {
	Id          RaffleTicket `json:"id"`
	Number      string       `json:"number"`
	PaymentHash string       `json:"paymentHash"`
}

type RaffleDrawCommit struct {
	SkippedTickets []RaffleTicket `json:"skippedTickets"`
}

type RafflePrizeWinners struct {
	Prize   string
	Tickets []RaffleDrawTicket
}

func raffleDrawTicket(ticket RaffleTicket) RaffleDrawTicket {
	paymentHash := string(ticket.paymentHash())
	return RaffleDrawTicket{
		Id:          ticket,
		Number:      ticket.number(),
		PaymentHash: paymentHash[0:5] + "…" + paymentHash[59:],
	}
}

func raffleDrawTickets(tickets []RaffleTicket) []RaffleDrawTicket {
	var drawTickets []RaffleDrawTicket
	for _, ticket := range tickets {
		drawTickets = append(drawTickets, raffleDrawTicket(ticket))
	}
	return drawTickets
}

func rafflePrizeWinners(raffle *Raffle, tickets []RaffleTicket) []RafflePrizeWinners {
	var prizeWinners []RafflePrizeWinners
	for _, prize := range raffle.Prizes {
		var drawTickets []RaffleDrawTicket
		for i := uint8(0); i < prize.Quantity; i++ {
			drawTickets = append(drawTickets, raffleDrawTicket(tickets[0]))
			tickets = tickets[1:]
		}
		prizeWinners = append(prizeWinners, RafflePrizeWinners{
			Prize:   prize.Name,
			Tickets: drawTickets,
		})
	}
	return prizeWinners
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
