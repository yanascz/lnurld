package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nbd-wtf/go-nostr"
	"log"
	"os"
)

func tagP() []string { return []string{"p", ""} }
func tagE() []string { return []string{"e", ""} }
func tagA() []string { return []string{"a", ""} }

func tagRelays() []string { return []string{"relays", ""} }
func tagAmount() []string { return []string{"amount", ""} }

func parseZapRequest(zapRequestJson string, amount string) (*nostr.Event, error) {
	var zapRequest nostr.Event
	if err := json.Unmarshal([]byte(zapRequestJson), &zapRequest); err != nil {
		return nil, err
	}

	if zapRequest.Kind != nostr.KindZapRequest {
		return nil, errors.New("not a zap request")
	}
	if len(zapRequest.Tags.GetAll(tagP())) != 1 {
		return nil, errors.New("invalid number of 'p' tags")
	}
	if len(zapRequest.Tags.GetAll(tagE())) > 1 {
		return nil, errors.New("invalid number of 'e' tags")
	}
	if len(zapRequest.Tags.GetAll(tagRelays())) != 1 {
		return nil, errors.New("invalid number of 'relays' tags")
	}
	if tag := zapRequest.Tags.GetFirst(tagAmount()); tag != nil && tag.Value() != amount {
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
		Tags:      zapRequest.Tags.GetAll(tagP()),
	}
	if e := zapRequest.Tags.GetFirst(tagE()); e != nil {
		zapReceipt.Tags = append(zapReceipt.Tags, *e)
	}
	if a := zapRequest.Tags.GetFirst(tagA()); a != nil {
		zapReceipt.Tags = append(zapReceipt.Tags, *a)
	}
	zapReceipt.Tags = append(zapReceipt.Tags, nostr.Tag{"bolt11", invoice.paymentRequest})
	zapReceipt.Tags = append(zapReceipt.Tags, nostr.Tag{"description", zapRequest.String()})

	if err := zapReceipt.Sign(service.privateKey); err != nil {
		log.Println("error signing zap receipt:", err)
		return
	}

	log.Println("publishing zap receipt", zapReceipt.ID, "for zap request", zapRequest.ID)
	service.publishEvent(&zapReceipt, (*zapRequest.Tags.GetFirst(tagRelays()))[1:])
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
