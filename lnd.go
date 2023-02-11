package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"github.com/mr-tron/base58"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
	"os"
	"time"
)

type LndConfig struct {
	Address      string
	CertFile     string `yaml:"cert-file"`
	MacaroonFile string `yaml:"macaroon-file"`
}

type Invoice struct {
	paymentHash    []byte
	paymentRequest string
	amount         int64
	settleDate     time.Time
	memo           string
}

func (invoice *Invoice) isSettled() bool {
	return !invoice.settleDate.IsZero()
}

func (invoice *Invoice) ticket() string {
	return base58.Encode(invoice.paymentHash)[0:7]
}

type LndClient struct {
	lnClient lnrpc.LightningClient
	ctx      context.Context
}

func newLndClient(config LndConfig) (*LndClient, error) {
	if config.CertFile == "" {
		return nil, fmt.Errorf("LND certificate file missing")
	}
	if config.MacaroonFile == "" {
		return nil, fmt.Errorf("LND macaroon file missing")
	}

	transportCredentials, err := credentials.NewClientTLSFromFile(config.CertFile, "")
	if err != nil {
		return nil, err
	}

	macaroonData, err := os.ReadFile(config.MacaroonFile)
	if err != nil {
		return nil, err
	}
	macaroonInstance := &macaroon.Macaroon{}
	if err := macaroonInstance.UnmarshalBinary(macaroonData); err != nil {
		return nil, err
	}
	macaroonCredentials, err := macaroons.NewMacaroonCredential(macaroonInstance)
	if err != nil {
		return nil, err
	}

	connection, err := grpc.Dial(config.Address,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithPerRPCCredentials(macaroonCredentials),
	)
	if err != nil {
		return nil, err
	}

	return &LndClient{
		lnClient: lnrpc.NewLightningClient(connection),
		ctx:      context.Background(),
	}, nil
}

func (client *LndClient) createInvoice(msats int64, memo string, descriptionHash []byte) (*Invoice, error) {
	lnInvoice := lnrpc.Invoice{
		Memo:            memo,
		DescriptionHash: descriptionHash,
		ValueMsat:       msats,
	}

	newLnInvoice, err := client.lnClient.AddInvoice(client.ctx, &lnInvoice)
	if err != nil {
		return nil, err
	}

	return &Invoice{
		paymentHash:    newLnInvoice.RHash,
		paymentRequest: newLnInvoice.PaymentRequest,
		amount:         msats / 1000,
	}, nil
}

func (client *LndClient) getInvoice(paymentHash string) (*Invoice, error) {
	paymentHashBytes, err := hex.DecodeString(paymentHash)
	if err != nil {
		return nil, err
	}

	lnPaymentHash := lnrpc.PaymentHash{
		RHash: paymentHashBytes,
	}

	lnInvoice, err := client.lnClient.LookupInvoice(client.ctx, &lnPaymentHash)
	if err != nil {
		return nil, err
	}

	var settleDate time.Time
	if lnInvoice.State == lnrpc.Invoice_SETTLED {
		settleDate = time.Unix(lnInvoice.SettleDate, 0)
	}

	return &Invoice{
		paymentHash:    lnInvoice.RHash,
		paymentRequest: lnInvoice.PaymentRequest,
		amount:         lnInvoice.Value,
		settleDate:     settleDate,
		memo:           lnInvoice.Memo,
	}, nil
}
