package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

const privateKey = "0000000000000000000000000000000000000000000000000000000000000001"

func createZapRequestJson(t *testing.T, kind int, tags nostr.Tags, tamperSignature bool) string {
	t.Helper()
	zapRequest := nostr.Event{CreatedAt: nostr.Now(), Kind: kind, Tags: tags}
	if err := zapRequest.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if tamperSignature {
		zapRequest.Sig = strings.Repeat("0", 128)
	}
	zapRequestJson, err := json.Marshal(zapRequest)
	if err != nil {
		t.Fatal(err)
	}
	return string(zapRequestJson)
}

func TestIsValidNpub(t *testing.T) {
	for _, c := range []struct {
		testName string
		npub     string
		expected bool
	}{
		{"empty", "", false},
		{"malformed", "not-an-npub", false},
		{"wrong_prefix", "nsec1qny3tkh0acurzla8x3zy4nhrjz5zd8l9sy9jys09umwng00manysew95gx", false},
		{"invalid", "npub1qny3tkh0acurzla8x3zy4nhrjz5zd8l9sy9jys09umwng00manysew95gf", false},
		{"valid", "npub1qny3tkh0acurzla8x3zy4nhrjz5zd8l9sy9jys09umwng00manysew95gx", true},
	} {
		t.Run(c.testName, func(t *testing.T) {
			assert.Equal(t, c.expected, isValidNpub(c.npub))
		})
	}
}

func TestParseZapRequest(t *testing.T) {
	npub := "npub1qny3tkh0acurzla8x3zy4nhrjz5zd8l9sy9jys09umwng00manysew95gx"
	publicKeyTag := nostr.Tag{"p", "04c915daefee38317fa734444acee390a8269fe5810b2241e5e6dd343dfbecc9"}
	relaysTag := nostr.Tag{"relays", "wss://relay.example.com"}
	eventTag := nostr.Tag{"e", "9ae37aa68f48645127299e9453eb5d908a0cbb6058ff340d528ed4d37c8994fb"}
	amountTag := nostr.Tag{"amount", "21"}

	t.Run("invalid_json", func(t *testing.T) {
		zapRequest, err := parseZapRequest("{", npub, "21")
		assert.Nil(t, zapRequest)
		assert.Error(t, err)
	})

	for _, c := range []struct {
		testName        string
		kind            int
		tags            nostr.Tags
		tamperSignature bool
		npub            string
		amount          string
		expectedError   string
	}{
		{"wrong_kind", nostr.KindTextNote, nostr.Tags{}, false, npub, "21", "not a zap request"},
		{"no_p_tag", nostr.KindZapRequest, nostr.Tags{}, false, npub, "21", "invalid number of 'p' tags"},
		{"too_many_p_tags", nostr.KindZapRequest, nostr.Tags{publicKeyTag, publicKeyTag}, false, npub, "21", "invalid number of 'p' tags"},
		{"wrong_p_tag", nostr.KindZapRequest, nostr.Tags{publicKeyTag}, false, "npub1cprv8ukeanvng59yeqd54df87ppndz75nqjn5jr7hkt8gfahxekqnnpg21", "21", "invalid 'p' tag"},
		{"too_many_e_tags", nostr.KindZapRequest, nostr.Tags{publicKeyTag, eventTag, eventTag}, false, npub, "21", "invalid number of 'e' tags"},
		{"no_relays_tag", nostr.KindZapRequest, nostr.Tags{publicKeyTag}, false, npub, "21", "invalid number of 'relays' tags"},
		{"too_many_relays_tags", nostr.KindZapRequest, nostr.Tags{publicKeyTag, relaysTag, relaysTag}, false, npub, "21", "invalid number of 'relays' tags"},
		{"amount_mismatch", nostr.KindZapRequest, nostr.Tags{publicKeyTag, relaysTag, amountTag}, false, npub, "42", "invalid 'amount' tag"},
		{"invalid_signature", nostr.KindZapRequest, nostr.Tags{publicKeyTag, relaysTag, amountTag}, true, npub, "21", "invalid signature"},
	} {
		t.Run(c.testName, func(t *testing.T) {
			zapRequestJson := createZapRequestJson(t, c.kind, c.tags, c.tamperSignature)
			zapRequest, err := parseZapRequest(zapRequestJson, c.npub, c.amount)
			assert.Nil(t, zapRequest)
			assert.ErrorContains(t, err, c.expectedError)
		})
	}

	t.Run("valid", func(t *testing.T) {
		zapRequestJson := createZapRequestJson(t, nostr.KindZapRequest, nostr.Tags{publicKeyTag, eventTag, relaysTag, amountTag}, false)
		zapRequest, err := parseZapRequest(zapRequestJson, npub, "21")
		assert.NoError(t, err)
		assert.NotNil(t, zapRequest)
	})
}
