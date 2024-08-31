package main

import "github.com/fiatjaf/go-lnurl"

const (
	payRequestTag      = "payRequest"
	withdrawRequestTag = "withdrawRequest"
	k1Param            = "k1"
	sigParam           = "sig"
	keyParam           = "key"
	amountParam        = "amount"
	nostrParam         = "nostr"
	commentParam       = "comment"
	prParam            = "pr"
)

type LnUrlPayParams struct {
	Callback        string `json:"callback"`
	MinSendable     int64  `json:"minSendable"`
	MaxSendable     int64  `json:"maxSendable"`
	EncodedMetadata string `json:"metadata"`
	CommentAllowed  int64  `json:"commentAllowed"`
	AllowsNostr     bool   `json:"allowsNostr"`
	NostrPubkey     string `json:"nostrPubkey,omitempty"`
	Tag             string `json:"tag"`
}

func successMessage(message string) *lnurl.SuccessAction {
	return &lnurl.SuccessAction{
		Tag:     "message",
		Message: message,
	}
}
