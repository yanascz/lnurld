package main

import (
	"context"
	"encoding/hex"
	"github.com/hashicorp/golang-lru/v2"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
	"log"
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

func (invoice *Invoice) getPaymentHash() string {
	return hex.EncodeToString(invoice.paymentHash)
}

func (invoice *Invoice) isSettled() bool {
	return !invoice.settleDate.IsZero()
}

type LndClient struct {
	lnClient lnrpc.LightningClient
	ctx      context.Context
	invoices *lru.Cache[string, Invoice]
}

func newLndClient(config LndConfig) *LndClient {
	if config.CertFile == "" {
		log.Fatal("LND certificate file missing")
	}
	if config.MacaroonFile == "" {
		log.Fatal("LND macaroon file missing")
	}

	transportCredentials, err := credentials.NewClientTLSFromFile(config.CertFile, "")
	if err != nil {
		log.Fatal(err)
	}

	macaroonData, err := os.ReadFile(config.MacaroonFile)
	if err != nil {
		log.Fatal(err)
	}
	macaroonInstance := &macaroon.Macaroon{}
	if err := macaroonInstance.UnmarshalBinary(macaroonData); err != nil {
		log.Fatal(err)
	}
	macaroonCredentials, err := macaroons.NewMacaroonCredential(macaroonInstance)
	if err != nil {
		log.Fatal(err)
	}

	connection, err := grpc.Dial(config.Address,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithPerRPCCredentials(macaroonCredentials),
	)
	if err != nil {
		log.Fatal(err)
	}

	invoices, err := lru.New[string, Invoice](1024)
	if err != nil {
		log.Fatal(err)
	}

	return &LndClient{
		lnClient: lnrpc.NewLightningClient(connection),
		ctx:      context.Background(),
		invoices: invoices,
	}
}

func (client *LndClient) createInvoice(msats int64, memo string, descriptionHash []byte) (*Invoice, error) {
	lnInvoice := lnrpc.Invoice{
		Memo:            memo,
		DescriptionHash: descriptionHash,
		ValueMsat:       msats,
		Expiry:          300,
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
	if invoice, invoiceCached := client.invoices.Get(paymentHash); invoiceCached {
		return &invoice, nil
	}

	paymentHashBytes, err := hex.DecodeString(paymentHash)
	if err != nil {
		return nil, err
	}

	lnPaymentHash := lnrpc.PaymentHash{RHash: paymentHashBytes}
	lnInvoice, err := client.lnClient.LookupInvoice(client.ctx, &lnPaymentHash)
	if err != nil {
		return nil, err
	}

	var settleDate time.Time
	if lnInvoice.State == lnrpc.Invoice_SETTLED {
		settleDate = time.Unix(lnInvoice.SettleDate, 0)
	}

	invoice := Invoice{
		paymentHash:    lnInvoice.RHash,
		paymentRequest: lnInvoice.PaymentRequest,
		amount:         lnInvoice.Value,
		settleDate:     settleDate,
		memo:           lnInvoice.Memo,
	}

	if lnInvoice.State == lnrpc.Invoice_SETTLED || lnInvoice.State == lnrpc.Invoice_CANCELED {
		client.invoices.Add(paymentHash, invoice)
	}

	return &invoice, nil
}

func (client *LndClient) decodePaymentRequest(paymentRequest string) (string, int64) {
	payReqString := lnrpc.PayReqString{PayReq: paymentRequest}
	payReq, err := client.lnClient.DecodePayReq(client.ctx, &payReqString)
	if err != nil {
		log.Println("error decoding payment request:", err)
		return "", 0
	}

	return payReq.PaymentHash, payReq.NumSatoshis
}

func (client *LndClient) sendPayment(paymentRequest string) error {
	sendRequest := lnrpc.SendRequest{PaymentRequest: paymentRequest}
	_, err := client.lnClient.SendPaymentSync(client.ctx, &sendRequest)

	return err
}
