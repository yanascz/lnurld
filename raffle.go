package main

import (
	"github.com/mr-tron/base58"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"math/rand"
	"sort"
	"strconv"
	"strings"
)

const (
	minQuantity = 1
	maxQuantity = 10
)

type RaffleId string

type Raffle struct {
	Id           RaffleId      `json:"-"`
	Owner        UserKey       `json:"owner"`
	IsMine       bool          `json:"-"`
	Title        string        `json:"title" binding:"min=1,max=50"`
	TicketPrice  int           `json:"ticketPrice" binding:"min=1,max=1000000"`
	FiatCurrency Currency      `json:"fiatCurrency" binding:"required"`
	Prizes       []RafflePrize `json:"prizes" binding:"min=1,max=21"`
}

func (raffle *Raffle) description(quantity int) string {
	return strconv.Itoa(quantity) + "× " + raffle.Title
}

func (raffle *Raffle) sendable(quantity int) int64 {
	return msats(quantity * raffle.TicketPrice)
}

func (raffle *Raffle) successMessage(tickets RaffleTickets) string {
	return raffle.Title + "\n" + tickets.numbers()
}

func (raffle *Raffle) PrizesCount() int {
	var prizesCount int
	for _, prize := range raffle.Prizes {
		prizesCount += prize.Quantity
	}
	return prizesCount
}

func (raffle *Raffle) prizes() []string {
	var prizes []string
	for _, prize := range raffle.Prizes {
		for i := 0; i < prize.Quantity; i++ {
			prizes = append(prizes, prize.Name)
		}
	}
	return prizes
}

type RafflePrize struct {
	Name     string `json:"name" binding:"min=1,max=50"`
	Quantity int    `json:"quantity" binding:"min=1,max=10"`
}

type RaffleQrCode struct {
	LnUrl string
	Uri   string
}

type RaffleTickets struct {
	paymentHash PaymentHash
	quantity    int
}

func parseRaffleTickets(value string) RaffleTickets {
	paymentHash, quantity, _ := strings.Cut(value, ",")
	return RaffleTickets{PaymentHash(paymentHash), max(1, parseInt(quantity))}
}

func (tickets RaffleTickets) String() string {
	return string(tickets.paymentHash) + "," + strconv.Itoa(tickets.quantity)
}

func (tickets RaffleTickets) numbers() string {
	var numbers []string
	symbols := raffleTicketSymbols(tickets.paymentHash)
	for i := 0; i < tickets.quantity; i++ {
		numbers = append(numbers, raffleTicketNumber(symbols, i))
	}
	sort.Slice(numbers, func(i, j int) bool {
		return strings.ToLower(numbers[i]) < strings.ToLower(numbers[j])
	})
	return "• " + strings.Join(numbers, "\n• ")
}

type RaffleTicket struct {
	paymentHash PaymentHash
	index       int
}

func parseRaffleTicket(value string) RaffleTicket {
	paymentHash, index, _ := strings.Cut(value, ":")
	return RaffleTicket{PaymentHash(paymentHash), parseInt(index)}
}

func (ticket RaffleTicket) String() string {
	return string(ticket.paymentHash) + ":" + strconv.Itoa(ticket.index)
}

func (ticket RaffleTicket) number() string {
	symbols := raffleTicketSymbols(ticket.paymentHash)
	return raffleTicketNumber(symbols, ticket.index)
}

func raffleTicketSymbols(paymentHash PaymentHash) string {
	return base58.Encode(paymentHash.bytes())
}

func raffleTicketNumber(symbols string, index int) string {
	return symbols[4*index : 4*index+5]
}

type RaffleDrawTicket struct {
	Id          string `json:"id"`
	Number      string `json:"number"`
	PaymentHash string `json:"paymentHash"`
}

type RaffleDrawCommit struct {
	SkippedTickets []string `json:"skippedTickets"`
}

type RafflePrizeWinners struct {
	Prize   string
	Tickets []RaffleDrawTicket
}

func shuffleRaffleDraw(raffleDraw []RaffleTicket) {
	rand.Shuffle(len(raffleDraw), func(i, j int) {
		raffleDraw[i], raffleDraw[j] = raffleDraw[j], raffleDraw[i]
	})
}

func raffleDrawTicket(ticket RaffleTicket) RaffleDrawTicket {
	paymentHash := string(ticket.paymentHash)
	return RaffleDrawTicket{
		Id:          ticket.String(),
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
		for i := 0; i < prize.Quantity; i++ {
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

var collator = collate.New(language.Czech, collate.Numeric)

func sortRaffles(raffles []*Raffle) []*Raffle {
	sort.Slice(raffles, func(i, j int) bool {
		raffleI, raffleJ := raffles[i], raffles[j]
		if raffleI.IsMine == raffleJ.IsMine {
			return collator.CompareString(raffleI.Title, raffleJ.Title) < 0
		}
		return raffleI.IsMine
	})
	return raffles
}

func parseInt(value string) int {
	if n, err := strconv.ParseInt(value, 10, 32); err == nil {
		return int(n)
	}
	return 0
}
