package main

import (
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mr-tron/base58"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
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

type RaffleStats struct {
	ticketsIssued     int
	ticketsPaid       int
	totalSatsReceived int64
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
	Id       string `json:"id"`
	Number   string `json:"number"`
	Preimage string `json:"preimage"`
}

type RaffleDrawCommit struct {
	SkippedTickets []string `json:"skippedTickets"`
}

type RafflePrizeWinners struct {
	Prize   string
	Tickets []RaffleDrawTicket
}

type RaffleRepository interface {
	getRaffleTickets(raffle *Raffle) []RaffleTickets
	isRaffleDrawAvailable(raffle *Raffle) bool
	createRaffleDraw(raffle *Raffle, tickets []RaffleTicket) error
	getRaffleDraw(raffle *Raffle) []RaffleTicket
	isRaffleDrawFinished(raffle *Raffle) bool
	createRaffleWinners(raffle *Raffle, tickets []RaffleTicket) error
	getRaffleWinners(raffle *Raffle) []RaffleTicket
}

type RaffleService struct {
	repository RaffleRepository
	lndClient  LndClient
}

func newRaffleService(repository RaffleRepository, lndClient LndClient) *RaffleService {
	return &RaffleService{repository: repository, lndClient: lndClient}
}

func (service *RaffleService) getRaffleStats(raffle *Raffle) RaffleStats {
	var ticketsIssued int
	var ticketsPaid int
	var totalSatsReceived int64
	for _, tickets := range service.repository.getRaffleTickets(raffle) {
		ticketsIssued += tickets.quantity
		invoice := service.lndClient.getInvoice(tickets.paymentHash)
		if invoice != nil && invoice.isSettled() {
			ticketsPaid += tickets.quantity
			totalSatsReceived += invoice.amount
		}
	}

	return RaffleStats{ticketsIssued, ticketsPaid, totalSatsReceived}
}

func (service *RaffleService) getRaffleDraw(raffle *Raffle) ([]RaffleTicket, error) {
	raffleDraw := service.repository.getRaffleDraw(raffle)
	if len(raffleDraw) > 0 {
		return raffleDraw, nil
	}

	for _, tickets := range service.repository.getRaffleTickets(raffle) {
		invoice := service.lndClient.getInvoice(tickets.paymentHash)
		if invoice != nil && invoice.isSettled() {
			for i := 0; i < tickets.quantity; i++ {
				raffleDraw = append(raffleDraw, RaffleTicket{tickets.paymentHash, i})
			}
		}
	}

	if len(raffleDraw) < raffle.PrizesCount() {
		return nil, &ClientError{message: "not enough tickets"}
	}

	shuffleRaffleTickets(raffleDraw) // separates tickets related to one invoice
	shuffleRaffleTickets(raffleDraw) // prepares the final draw

	if err := service.repository.createRaffleDraw(raffle, raffleDraw); err != nil {
		return nil, &ServerError{message: "storing raffle draw", cause: err}
	}

	return raffleDraw, nil
}

func (service *RaffleService) getDrawnTickets(raffle *Raffle) ([]RaffleDrawTicket, error) {
	raffleDraw, err := service.getRaffleDraw(raffle)
	if err != nil {
		return nil, err
	}

	var drawnTickets []RaffleDrawTicket
	for _, ticket := range raffleDraw {
		drawnTickets = append(drawnTickets, service.raffleDrawTicket(ticket))
	}

	return drawnTickets, nil
}

func (service *RaffleService) commitRaffleDraw(raffle *Raffle, skippedTickets []string) error {
	if !service.repository.isRaffleDrawAvailable(raffle) || service.repository.isRaffleDrawFinished(raffle) {
		return &ClientError{message: "not committable"}
	}

	raffleDraw := service.repository.getRaffleDraw(raffle)
	remainingTickets := slices.DeleteFunc(raffleDraw, func(ticket RaffleTicket) bool {
		if slices.Contains(skippedTickets, ticket.String()) {
			skippedTickets = skippedTickets[1:]
			return true
		}
		return false
	})

	prizesCount := raffle.PrizesCount()
	if len(remainingTickets) < prizesCount || len(skippedTickets) > 0 {
		return &ClientError{message: "invalid commit request"}
	}

	raffleWinners := remainingTickets[0:prizesCount]
	slices.Reverse(raffleWinners)

	if err := service.repository.createRaffleWinners(raffle, raffleWinners); err != nil {
		return &ServerError{message: "storing raffle winners", cause: err}
	}

	return nil
}

func (service *RaffleService) getPrizeWinners(raffle *Raffle) []RafflePrizeWinners {
	if !service.repository.isRaffleDrawFinished(raffle) {
		return nil
	}

	var prizeWinners []RafflePrizeWinners
	raffleWinners := service.repository.getRaffleWinners(raffle)
	for _, prize := range raffle.Prizes {
		var tickets []RaffleDrawTicket
		for i := 0; i < prize.Quantity; i++ {
			tickets = append(tickets, service.raffleDrawTicket(raffleWinners[0]))
			raffleWinners = raffleWinners[1:]
		}
		prizeWinners = append(prizeWinners, RafflePrizeWinners{
			Prize:   prize.Name,
			Tickets: tickets,
		})
	}

	return prizeWinners
}

func (service *RaffleService) raffleDrawTicket(ticket RaffleTicket) RaffleDrawTicket {
	invoice := service.lndClient.getInvoice(ticket.paymentHash)
	return RaffleDrawTicket{
		Id:       ticket.String(),
		Number:   ticket.number(),
		Preimage: invoice.preimage[0:5] + "…" + invoice.preimage[59:],
	}
}

func shuffleRaffleTickets(raffleDraw []RaffleTicket) {
	rand.Shuffle(len(raffleDraw), func(i, j int) {
		raffleDraw[i], raffleDraw[j] = raffleDraw[j], raffleDraw[i]
	})
}

func sortRaffles(raffles []*Raffle) []*Raffle {
	collator := collate.New(language.Czech, collate.Numeric)
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
