package main

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log"
	"os"
	"slices"

	"github.com/nbd-wtf/go-nostr"
)

const (
	tagPublicKey   = "p"
	tagEvent       = "e"
	tagAddress     = "a"
	tagRelays      = "relays"
	tagAmount      = "amount"
	tagBolt11      = "bolt11"
	tagDescription = "description"
)

func countTags(tags iter.Seq[nostr.Tag]) int {
	count := 0
	for range tags {
		count++
	}
	return count
}

func parseZapRequest(zapRequestJson string, amount string) (*nostr.Event, error) {
	var zapRequest nostr.Event
	if err := json.Unmarshal([]byte(zapRequestJson), &zapRequest); err != nil {
		return nil, err
	}

	if zapRequest.Kind != nostr.KindZapRequest {
		return nil, errors.New("not a zap request")
	}
	if countTags(zapRequest.Tags.FindAll(tagPublicKey)) != 1 {
		return nil, errors.New("invalid number of 'p' tags")
	}
	if countTags(zapRequest.Tags.FindAll(tagEvent)) > 1 {
		return nil, errors.New("invalid number of 'e' tags")
	}
	if countTags(zapRequest.Tags.FindAll(tagRelays)) != 1 {
		return nil, errors.New("invalid number of 'relays' tags")
	}
	if tag := zapRequest.Tags.Find(tagAmount); tag != nil && tag[1] != amount {
		return nil, errors.New("invalid 'amount' tag")
	}
	if valid, _ := zapRequest.CheckSignature(); !valid {
		return nil, errors.New("invalid signature")
	}

	return &zapRequest, nil
}

type NostrConfig struct {
	Relays []string
}

type NostrService struct {
	privateKey string
	relays     []string
}

func newNostrService(dataDir string, config NostrConfig) *NostrService {
	var privateKey string
	privateKeyFileName := dataDir + ".nostr"

	if privateKeyBytes, err := os.ReadFile(privateKeyFileName); err == nil {
		privateKey = string(privateKeyBytes)
	} else {
		privateKey = nostr.GeneratePrivateKey()
		if privateKey == "" {
			log.Fatal("error creating Nostr private key")
		}
		if err := os.WriteFile(privateKeyFileName, []byte(privateKey), 0400); err != nil {
			log.Fatal(err)
		}
	}

	return &NostrService{
		privateKey: privateKey,
		relays:     config.Relays,
	}
}

func (service *NostrService) getPublicKey() string {
	publicKey, _ := nostr.GetPublicKey(service.privateKey)
	return publicKey
}

func (service *NostrService) publishZapReceipt(zapRequest *nostr.Event, invoice *Invoice) {
	zapReceipt := nostr.Event{
		PubKey:    service.getPublicKey(),
		CreatedAt: nostr.Timestamp(invoice.settleDate.Unix()),
		Kind:      nostr.KindZap,
		Tags:      slices.Collect(zapRequest.Tags.FindAll(tagPublicKey)),
	}
	if e := zapRequest.Tags.Find(tagEvent); e != nil {
		zapReceipt.Tags = append(zapReceipt.Tags, e)
	}
	if a := zapRequest.Tags.Find(tagAddress); a != nil {
		zapReceipt.Tags = append(zapReceipt.Tags, a)
	}
	zapReceipt.Tags = append(zapReceipt.Tags, nostr.Tag{tagBolt11, invoice.paymentRequest})
	zapReceipt.Tags = append(zapReceipt.Tags, nostr.Tag{tagDescription, zapRequest.String()})

	if err := zapReceipt.Sign(service.privateKey); err != nil {
		log.Println("error signing zap receipt:", err)
		return
	}

	log.Println("publishing zap receipt", zapReceipt.ID, "for zap request", zapRequest.ID)
	service.publishEvent(&zapReceipt, zapRequest.Tags.Find(tagRelays)[1:])
}

func (service *NostrService) publishEvent(event *nostr.Event, additionalRelays []string) {
	alreadyPublished := map[string]bool{}
	for _, relay := range append(service.relays, additionalRelays...) {
		if !alreadyPublished[relay] {
			go publishEvent(event, relay)
			alreadyPublished[relay] = true
		}
	}
}

func publishEvent(event *nostr.Event, url string) {
	relay, err := nostr.RelayConnect(context.Background(), url)
	if err != nil {
		log.Println("error connecting to relay:", err)
		return
	}
	defer relay.Close()

	if err := relay.Publish(context.Background(), *event); err != nil {
		log.Println("error publishing event to "+url+":", err)
	}
}
