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

const invoiceExpiryInSeconds = 300

type LndConfig struct {
	Address      string
	CertFile     string `yaml:"cert-file"`
	MacaroonFile string `yaml:"macaroon-file"`
	CacheSize    uint16 `yaml:"cache-size"`
}

type PaymentHash string

func toPaymentHash(value string) PaymentHash {
	return PaymentHash(value)
}

func (paymentHash PaymentHash) String() string {
	return string(paymentHash)
}

func (paymentHash PaymentHash) bytes() []byte {
	if bytes, err := hex.DecodeString(string(paymentHash)); err == nil {
		return bytes
	}
	return nil
}

type Invoice struct {
	preimage       string
	paymentHash    PaymentHash
	paymentRequest string
	amount         int64
	settleDate     time.Time
	memo           string
}

func (invoice *Invoice) isSettled() bool {
	return !invoice.settleDate.IsZero()
}

type LndClient struct {
	lnClient lnrpc.LightningClient
	ctx      context.Context
	invoices *lru.Cache[PaymentHash, Invoice]
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

	invoices, err := lru.New[PaymentHash, Invoice](int(config.CacheSize))
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
		Expiry:          invoiceExpiryInSeconds,
	}

	newLnInvoice, err := client.lnClient.AddInvoice(client.ctx, &lnInvoice)
	if err != nil {
		return nil, err
	}

	return &Invoice{
		preimage:       hex.EncodeToString(lnInvoice.RPreimage),
		paymentHash:    PaymentHash(hex.EncodeToString(newLnInvoice.RHash)),
		paymentRequest: newLnInvoice.PaymentRequest,
		amount:         msats / 1000,
	}, nil
}

func (client *LndClient) getInvoice(paymentHash PaymentHash) *Invoice {
	if invoice, invoiceCached := client.invoices.Get(paymentHash); invoiceCached {
		return &invoice
	}

	lnPaymentHash := lnrpc.PaymentHash{RHash: paymentHash.bytes()}
	lnInvoice, err := client.lnClient.LookupInvoice(client.ctx, &lnPaymentHash)
	if err != nil {
		log.Println("error looking up invoice:", err)
		return nil
	}

	var settleDate time.Time
	if lnInvoice.State == lnrpc.Invoice_SETTLED {
		settleDate = time.Unix(lnInvoice.SettleDate, 0)
	}

	invoice := Invoice{
		preimage:       hex.EncodeToString(lnInvoice.RPreimage),
		paymentHash:    paymentHash,
		paymentRequest: lnInvoice.PaymentRequest,
		amount:         lnInvoice.Value,
		settleDate:     settleDate,
		memo:           lnInvoice.Memo,
	}

	if lnInvoice.State == lnrpc.Invoice_SETTLED || lnInvoice.State == lnrpc.Invoice_CANCELED {
		client.invoices.Add(paymentHash, invoice)
	}

	return &invoice
}

func (client *LndClient) decodePaymentRequest(paymentRequest string) (PaymentHash, int64) {
	payReqString := lnrpc.PayReqString{PayReq: paymentRequest}
	payReq, err := client.lnClient.DecodePayReq(client.ctx, &payReqString)
	if err != nil {
		log.Println("error decoding payment request:", err)
		return "", 0
	}

	return PaymentHash(payReq.PaymentHash), payReq.NumSatoshis
}

func (client *LndClient) sendPayment(paymentRequest string, feeLimit int64) error {
	sendRequest := lnrpc.SendRequest{
		PaymentRequest: paymentRequest,
		FeeLimit:       &lnrpc.FeeLimit{Limit: &lnrpc.FeeLimit_Fixed{Fixed: feeLimit}},
	}
	_, err := client.lnClient.SendPaymentSync(client.ctx, &sendRequest)

	return err
}
