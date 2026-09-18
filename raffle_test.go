package main

import (
	"github.com/stretchr/testify/assert"
	"slices"
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

func TestRemoveSkippedTickets(t *testing.T) {
	paymentHash := PaymentHash("d643d24061a5410f96693978711071819a9700d38b006285246c8e227e32fd4d")
	raffleDraw := []RaffleTicket{
		{paymentHash, 0}, {paymentHash, 1}, {paymentHash, 2}, {paymentHash, 3}, {paymentHash, 4},
	}

	for _, c := range []struct {
		testName        string
		skippedTickets  []string
		expectedTickets []RaffleTicket
		expectedFound   bool
	}{
		{
			"nothing_skipped", nil,
			[]RaffleTicket{{paymentHash, 0}, {paymentHash, 1}, {paymentHash, 2}, {paymentHash, 3}, {paymentHash, 4}},
			true,
		},
		{
			"first_skipped", []string{string(paymentHash) + ":0"},
			[]RaffleTicket{{paymentHash, 1}, {paymentHash, 2}, {paymentHash, 3}, {paymentHash, 4}},
			true,
		},
		{
			"several_skipped", []string{string(paymentHash) + ":1", string(paymentHash) + ":3"},
			[]RaffleTicket{{paymentHash, 0}, {paymentHash, 2}, {paymentHash, 4}},
			true,
		},
		{
			"all_skipped", []string{
				string(paymentHash) + ":0", string(paymentHash) + ":1", string(paymentHash) + ":2",
				string(paymentHash) + ":3", string(paymentHash) + ":4",
			},
			[]RaffleTicket{},
			true,
		},
		{
			"unknown_skipped", []string{string(paymentHash) + ":9"},
			[]RaffleTicket{{paymentHash, 0}, {paymentHash, 1}, {paymentHash, 2}, {paymentHash, 3}, {paymentHash, 4}},
			false,
		},
	} {
		t.Run(c.testName, func(t *testing.T) {
			tickets, found := removeSkippedTickets(slices.Clone(raffleDraw), c.skippedTickets)
			assert.Equal(t, c.expectedTickets, tickets)
			assert.Equal(t, c.expectedFound, found)
		})
	}
}

func TestShortenPreimage(t *testing.T) {
	preimage := "8c8d4bc4a33d52b52dcbcbd4b6da95e2b9dd34f12a54de3bb6faf03f4f3dc1a7"
	assert.Equal(t, "8c8d4…dc1a7", shortenPreimage(preimage))
	assert.Equal(t, "", shortenPreimage(""))
	assert.Equal(t, "abc", shortenPreimage("abc"))
}
