package main

import (
	"github.com/stretchr/testify/assert"
	"strconv"
	"testing"
)

func TestRaffleTickets(t *testing.T) {
	paymentHash := PaymentHash("d643d24061a5410f96693978711071819a9700d38b006285246c8e227e32fd4d")
	for _, c := range []struct {
		testName         string
		suffix           string
		expectedQuantity int
		expectedNumbers  string
	}{
		{"no_quantity", "", 1, "FRQEG"},
		{"quantity=1", ",1", 1, "FRQEG"},
		{"quantity=2", ",2", 2, "FRQEG Gk7zz"},
		{"quantity=10", ",10", 10, "aPHsr CcdyC CVyiK FRQEG Gk7zz KWPnD Nenno oUAMC r1YiN z758a"},
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

func TestIsValidRaffleAmount(t *testing.T) {
	for _, c := range []struct {
		testName       string
		amount         int64
		expectedResult bool
	}{
		{"negative", -21000, false},
		{"zero", 0, false},
		{"non-divisible", 20999, false},
		{"one_ticket", 21000, true},
		{"ten_tickets", 210000, true},
		{"eleven_tickets", 231000, false},
	} {
		t.Run(c.testName, func(t *testing.T) {
			assert.Equal(t, c.expectedResult, isValidRaffleAmount(c.amount, 21000))
		})
	}
}
