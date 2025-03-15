package main

import (
	"github.com/stretchr/testify/assert"
	"strconv"
	"testing"
)

func TestRaffle(t *testing.T) {
	raffle := Raffle{
		Title:       "Lightning Raffle",
		TicketPrice: 21,
		Prizes:      []RafflePrize{{"Trezor", 1}, {"Book", 2}, {"Stickers", 3}},
	}
	tickets := RaffleTickets{
		paymentHash: PaymentHash("d643d24061a5410f96693978711071819a9700d38b006285246c8e227e32fd4d"),
		quantity:    3,
	}
	assert.Equal(t, "3× Lightning Raffle", raffle.description(3))
	assert.Equal(t, int64(147000), raffle.sendable(7))
	assert.Equal(t, "Lightning Raffle\n• FRQEG\n• Gk7zz\n• z758a", raffle.successMessage(tickets))
	assert.Equal(t, 6, raffle.PrizesCount())
	assert.Equal(t, []string{"Trezor", "Book", "Book", "Stickers", "Stickers", "Stickers"}, raffle.prizes())
}

func TestRaffleTickets(t *testing.T) {
	paymentHash := PaymentHash("d643d24061a5410f96693978711071819a9700d38b006285246c8e227e32fd4d")
	for _, c := range []struct {
		testName         string
		suffix           string
		expectedQuantity int
		expectedNumbers  string
	}{
		{"no_quantity", "", 1, "• FRQEG"},
		{"quantity=1", ",1", 1, "• FRQEG"},
		{"quantity=2", ",2", 2, "• FRQEG\n• Gk7zz"},
		{"quantity=10", ",10", 10, "• aPHsr\n• CcdyC\n• CVyiK\n• FRQEG\n• Gk7zz\n• KWPnD\n• Nenno\n• oUAMC\n• r1YiN\n• z758a"},
	} {
		t.Run(c.testName, func(t *testing.T) {
			tickets := parseRaffleTickets(string(paymentHash) + c.suffix)
			assert.Equal(t, paymentHash, tickets.paymentHash)
			assert.Equal(t, c.expectedQuantity, tickets.quantity)
			assert.Equal(t, string(paymentHash)+","+strconv.Itoa(c.expectedQuantity), tickets.String())
			assert.Equal(t, c.expectedNumbers, tickets.numbers())
		})
	}
}

func TestRaffleTicket(t *testing.T) {
	paymentHash := PaymentHash("a5506d48d2e456769e4f557d440e8e502c815e6670bfb6a4299d136a52db54fd")
	for _, c := range []struct {
		testName       string
		suffix         string
		expectedIndex  int
		expectedNumber string
	}{
		{"no_index", "", 0, "C8KQC"},
		{"index=0", ":0", 0, "C8KQC"},
		{"index=1", ":1", 1, "CsoRG"},
		{"index=9", ":9", 9, "soGi8"},
	} {
		t.Run(c.testName, func(t *testing.T) {
			ticket := parseRaffleTicket(string(paymentHash) + c.suffix)
			assert.Equal(t, paymentHash, ticket.paymentHash)
			assert.Equal(t, c.expectedIndex, ticket.index)
			assert.Equal(t, string(paymentHash)+":"+strconv.Itoa(c.expectedIndex), ticket.String())
			assert.Equal(t, c.expectedNumber, ticket.number())
		})
	}
}

func TestSortRaffles(t *testing.T) {
	raffles := []*Raffle{{Title: "Raffle #1"}, {Title: "Raffle #11"}, {Title: "Raffle #2"}}
	assert.Equal(t, []*Raffle{{Title: "Raffle #1"}, {Title: "Raffle #2"}, {Title: "Raffle #11"}}, sortRaffles(raffles))
}
