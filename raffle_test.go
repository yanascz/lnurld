package main

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

type RaffleRepositoryMock struct {
	tickets map[RaffleId][]RaffleTickets
	draws   map[RaffleId][]RaffleTicket
	winners map[RaffleId][]RaffleTicket
}

func (r *RaffleRepositoryMock) reset() {
	r.draws = map[RaffleId][]RaffleTicket{}
	r.winners = map[RaffleId][]RaffleTicket{}
}
func (r *RaffleRepositoryMock) getRaffleTickets(raffle *Raffle) []RaffleTickets {
	return r.tickets[raffle.Id]
}
func (r *RaffleRepositoryMock) isRaffleDrawAvailable(raffle *Raffle) bool {
	return len(r.draws[raffle.Id]) > 0
}
func (r *RaffleRepositoryMock) createRaffleDraw(raffle *Raffle, tickets []RaffleTicket) error {
	r.draws[raffle.Id] = tickets
	return nil
}
func (r *RaffleRepositoryMock) getRaffleDraw(raffle *Raffle) []RaffleTicket {
	return append([]RaffleTicket{}, r.draws[raffle.Id]...)
}
func (r *RaffleRepositoryMock) isRaffleDrawFinished(raffle *Raffle) bool {
	return len(r.winners[raffle.Id]) > 0
}
func (r *RaffleRepositoryMock) createRaffleWinners(raffle *Raffle, tickets []RaffleTicket) error {
	r.winners[raffle.Id] = tickets
	return nil
}
func (r *RaffleRepositoryMock) getRaffleWinners(raffle *Raffle) []RaffleTicket {
	return append([]RaffleTicket{}, r.winners[raffle.Id]...)
}

type LndClientMock struct {
	invoices map[PaymentHash]Invoice
}

func (c *LndClientMock) createInvoice(_ int64, _ string, _ []byte) (*Invoice, error) {
	return nil, errors.New("not implemented")
}
func (c *LndClientMock) getInvoice(paymentHash PaymentHash) *Invoice {
	if invoice, exists := c.invoices[paymentHash]; exists {
		invoice.paymentHash = paymentHash
		return &invoice
	}
	return nil
}
func (c *LndClientMock) decodePaymentRequest(_ string) (PaymentHash, int64) {
	return "", 0
}
func (c *LndClientMock) sendPayment(_ string, _ int64) error {
	return errors.New("not implemented")
}

func TestRaffleService(t *testing.T) {
	raffleWithThreePrizes := &Raffle{Id: "raffle", Prizes: []RafflePrize{{"Trezor", 1}, {"Book", 2}}}
	raffleWithElevenPrizes := &Raffle{Id: "raffle", Prizes: []RafflePrize{{"Trezor", 1}, {"Book", 3}, {"Stickers", 7}}}

	raffleRepositoryMock := RaffleRepositoryMock{
		tickets: map[RaffleId][]RaffleTickets{
			"raffle": {
				{"743aac59f19edbdd71e764143440e8265ca57cccce72ec742e0fdd007ff9d535", 1},
				{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 3},
				{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 2},
				{"a5a0c695a52d15a2b83ca8de9ece8e64d4044a696cffbe4a3d9d01e8fb259f17", 2},
				{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 4},
			},
		},
	}
	lndClientMock := LndClientMock{
		invoices: map[PaymentHash]Invoice{
			"743aac59f19edbdd71e764143440e8265ca57cccce72ec742e0fdd007ff9d535": {
				preimage: "698beef8fa55f0735e3d7e833acaf427b4ecf9d3905bac2923d8169a5e785d16",
				amount:   21,
			},
			"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a": {
				preimage:   "5944264c5b79d96961fcc12fd788598667423d918529bae52072311d2b0b6963",
				amount:     63,
				settleDate: time.Now(),
			},
			"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3": {
				preimage:   "5569d83ccbd164694668e562950c67d85c78024f66c521678c2492a904de2f48",
				amount:     42,
				settleDate: time.Now(),
			},
			"a5a0c695a52d15a2b83ca8de9ece8e64d4044a696cffbe4a3d9d01e8fb259f17": {
				preimage: "7bc1eefc0634732758a2ba29473523410520adcf7679740de978889d34336842",
				amount:   42,
			},
			"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c": {
				preimage:   "b05d105981c9ca5a4055aa0ee4b0960fd77a588a6194eb6217493af6c2469836",
				amount:     84,
				settleDate: time.Now(),
			},
		}}

	service := newRaffleService(&raffleRepositoryMock, &lndClientMock)

	t.Run("getRaffleStats", func(t *testing.T) {
		stats := service.getRaffleStats(raffleWithThreePrizes)
		assert.Equal(t, 12, stats.ticketsIssued)
		assert.Equal(t, 9, stats.ticketsPaid)
		assert.Equal(t, int64(189), stats.totalSatsReceived)
	})

	t.Run("getRaffleDraw", func(t *testing.T) {
		raffleRepositoryMock.reset()
		raffleTickets := []RaffleTicket{
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 0},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 1},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 2},
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 0},
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 1},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 0},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 1},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 2},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 3},
		}
		_, err := service.getRaffleDraw(raffleWithElevenPrizes)
		assert.ErrorContains(t, err, "not enough tickets")
		createdRaffleDraw, _ := service.getRaffleDraw(raffleWithThreePrizes)
		assert.ElementsMatch(t, createdRaffleDraw, raffleTickets)
		assert.NotEqual(t, raffleTickets, createdRaffleDraw)
		existingRaffleDraw, _ := service.getRaffleDraw(raffleWithThreePrizes)
		assert.Equal(t, createdRaffleDraw, existingRaffleDraw)
	})

	t.Run("getDrawnTickets", func(t *testing.T) {
		raffleRepositoryMock.reset()
		_ = raffleRepositoryMock.createRaffleDraw(raffleWithThreePrizes, []RaffleTicket{
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 1},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 3},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 2},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 0},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 1},
		})
		drawnTickets, _ := service.getDrawnTickets(raffleWithThreePrizes)
		assert.Equal(t, []RaffleDrawTicket{
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3:1", "54QhT", "5569d…e2f48"},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c:3", "356qg", "b05d1…69836"},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a:2", "aUx4k", "59442…b6963"},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c:0", "2aWvW", "b05d1…69836"},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a:1", "vdJua", "59442…b6963"},
		}, drawnTickets)
	})

	t.Run("commitRaffleDraw", func(t *testing.T) {
		raffleRepositoryMock.reset()
		assert.ErrorContains(t, service.commitRaffleDraw(raffleWithThreePrizes, []string{}), "not committable")
		_ = raffleRepositoryMock.createRaffleDraw(raffleWithThreePrizes, []RaffleTicket{
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 1},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 3},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 2},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 0},
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 1},
		})
		assert.ErrorContains(t, service.commitRaffleDraw(raffleWithThreePrizes, []string{
			"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3:0",
		}), "invalid commit request") // ticket not present in the draw
		assert.ErrorContains(t, service.commitRaffleDraw(raffleWithThreePrizes, []string{
			"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c:3",
			"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a:2",
			"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a:1",
		}), "invalid commit request") // too many tickets skipped
		assert.Nil(t, service.commitRaffleDraw(raffleWithThreePrizes, []string{
			"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c:3",
			"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a:2",
		}))
		assert.Equal(t, []RaffleTicket{
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 1},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 0},
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 1},
		}, raffleRepositoryMock.getRaffleWinners(raffleWithThreePrizes))
		assert.ErrorContains(t, service.commitRaffleDraw(raffleWithThreePrizes, []string{}), "not committable")
	})

	t.Run("getPrizeWinners", func(t *testing.T) {
		raffleRepositoryMock.reset()
		assert.Nil(t, service.getPrizeWinners(raffleWithThreePrizes))
		_ = raffleRepositoryMock.createRaffleWinners(raffleWithThreePrizes, []RaffleTicket{
			{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a", 2},
			{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c", 3},
			{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3", 1},
		})
		prizeWinners := service.getPrizeWinners(raffleWithThreePrizes)
		assert.Equal(t, []RafflePrizeWinners{
			{Prize: "Trezor", Tickets: []RaffleDrawTicket{
				{"45a7bd7ff196941f8faa49ead2437db6ff67cdb8a29494fde621b7f35fead46a:2", "aUx4k", "59442…b6963"},
			}},
			{Prize: "Book", Tickets: []RaffleDrawTicket{
				{"1771afe9f919e654b91ab36b97afd94ffad65b18ab8a8e5d769eccef8f12759c:3", "356qg", "b05d1…69836"},
				{"b5b8d00d46de45b71a10d1d8ecbb62fb665b6bcc5a5863a0fa8c56b5c3f3c5e3:1", "54QhT", "5569d…e2f48"},
			}},
		}, prizeWinners)
	})
}

func TestSortRaffles(t *testing.T) {
	t.Run("ordering", func(t *testing.T) {
		raffles := []*Raffle{{Title: "Raffle #1"}, {Title: "Raffle #11"}, {Title: "Raffle #2"}}
		assert.Equal(t, []*Raffle{{Title: "Raffle #1"}, {Title: "Raffle #2"}, {Title: "Raffle #11"}}, sortRaffles(raffles))
	})

	t.Run("concurrency", func(t *testing.T) {
		var waitGroup sync.WaitGroup
		for i := 0; i < 8; i++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				for j := 0; j < 200; j++ {
					sortRaffles([]*Raffle{
						{Title: "Žluťoučký kůň"},
						{Title: "Ampérmetr"},
						{Title: "Čokoláda"},
						{Title: "Šiška"},
					})
				}
			}()
		}
		waitGroup.Wait()
	})
}
