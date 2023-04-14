package main

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"flag"
	"github.com/fiatjaf/go-lnurl"
	"github.com/gin-gonic/gin"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type LnAccount struct {
	AccountKey        string
	Currency          Currency
	InvoicesIssued    int
	InvoicesSettled   int
	TotalSatsReceived int64
	TotalFiatReceived float64
	Archivable        bool
	Raffle            *LnAccountRaffle
	Comments          []LnAccountComment
}

type LnAccountRaffle struct {
	TicketPrice uint32
	PrizesCount int
}

type LnAccountComment struct {
	Amount     int64
	SettleDate time.Time
	Comment    string
}

type LnRaffle struct {
	Prizes       []string
	DrawnTickets []LnRaffleTicket
}

type LnRaffleTicket struct {
	Number      string `json:"number"`
	PaymentHash string `json:"paymentHash"`
}

type LnTerminal struct {
	AccountKey string
	Currency   Currency
	Title      string
}

type LnCreateInvoice struct {
	AccountKey string `json:"accountKey"`
	Amount     string `json:"amount"`
}

type LnInvoice struct {
	PaymentHash string `json:"paymentHash"`
	QrCode      string `json:"qrCode"`
}

type LnInvoiceStatus struct {
	Settled bool `json:"settled"`
}

var (
	//go:embed files/static
	staticFs embed.FS
	//go:embed files/templates
	templatesFs embed.FS

	config       *Config
	repository   *Repository
	lndClient    *LndClient
	ratesService *RatesService
)

func main() {
	var configFileName string

	flagSet := flag.NewFlagSet("LNURL Daemon", flag.ExitOnError)
	flagSet.StringVar(&configFileName, "config", "/etc/lnurld/config.yaml", "Path to a YAML config file.")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	config = loadConfig(configFileName)
	repository = newRepository(config.ThumbnailDir, config.DataDir)
	lndClient = newLndClient(config.Lnd)
	ratesService = newRatesService(21 * time.Second)

	lnurld := gin.Default()
	_ = lnurld.SetTrustedProxies(nil)
	loadTemplates(lnurld, "files/templates/*.gohtml")

	lnurld.GET("/.well-known/lnurlp/:name", lnPayHandler)
	lnurld.GET("/ln/pay/:name", lnPayHandler)
	lnurld.GET("/ln/pay/:name/qr-code", lnPayQrCodeHandler)

	authorized := lnurld.Group("/", gin.BasicAuth(config.Credentials))
	authorized.GET("/ln/static/*filepath", lnStaticFileHandler)
	authorized.GET("/ln/accounts", lnAccountsHandler)
	authorized.GET("/ln/accounts/:name", lnAccountHandler)
	authorized.GET("/ln/accounts/:name/raffle", lnAccountRaffleHandler)
	authorized.GET("/ln/accounts/:name/terminal", lnAccountTerminalHandler)
	authorized.POST("/ln/accounts/:name/archive", lnAccountArchiveHandler)
	authorized.POST("/ln/invoices", lnInvoicesHandler)
	authorized.GET("/ln/invoices/:paymentHash", lnInvoiceStatusHandler)

	log.Fatal(lnurld.Run(config.Listen))
}

func lnPayHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}

	scheme, host := getSchemeAndHost(context)
	lnurlMetadata := lnurl.Metadata{
		Description:      account.Description,
		LightningAddress: accountKey + "@" + host,
		IsEmail:          account.IsAlsoEmail,
	}

	if account.Thumbnail != "" {
		thumbnail, err := repository.loadThumbnail(account.Thumbnail)
		if err != nil {
			log.Println("Thumbnail not readable:", err)
		} else {
			lnurlMetadata.Image.Bytes = thumbnail.bytes
			lnurlMetadata.Image.Ext = thumbnail.ext
		}
	}

	amount := context.Query("amount")
	if amount == "" {
		context.JSON(http.StatusOK, lnurl.LNURLPayParams{
			Callback:        scheme + "://" + host + context.Request.RequestURI,
			MaxSendable:     account.getMaxSendable(),
			MinSendable:     account.getMinSendable(),
			EncodedMetadata: lnurlMetadata.Encode(),
			CommentAllowed:  int64(account.CommentAllowed),
			Tag:             "payRequest",
		})
		return
	}

	msats, err := strconv.ParseInt(amount, 10, 64)
	if err != nil || msats < account.getMinSendable() || msats > account.getMaxSendable() {
		log.Println("Invalid amount:", amount)
		context.JSON(http.StatusBadRequest, lnurl.ErrorResponse("Invalid Amount"))
		return
	}

	comment := context.Query("comment")
	if commentLength := len(comment); commentLength > int(account.CommentAllowed) {
		log.Println("Invalid comment length:", commentLength)
		context.JSON(http.StatusBadRequest, lnurl.ErrorResponse("Invalid comment length"))
		return
	}

	metadataHash := sha256.Sum256([]byte(lnurlMetadata.Encode()))
	invoice := createInvoice(context, accountKey, msats, comment, metadataHash[:])
	if invoice == nil {
		return
	}

	successMessage := "Thanks, payment received!"
	if account.Raffle != nil {
		successMessage = "Ticket " + invoice.getTicketNumber()
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: &lnurl.SuccessAction{Tag: "message", Message: successMessage},
		Routes:        []string{},
	})
}

func lnPayQrCodeHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}

	requestedSize := context.DefaultQuery("size", "256")
	size, err := strconv.ParseUint(requestedSize, 10, 12)
	if err != nil {
		context.String(http.StatusBadRequest, "400 bad request")
		return
	}

	scheme, host := getSchemeAndHost(context)
	actualLnUrl := scheme + "://" + host + "/ln/pay/" + accountKey
	encodedLnUrl, err := lnurl.LNURLEncode(actualLnUrl)
	if err != nil {
		log.Println("Error encoding LNURL:", err)
		context.String(http.StatusInternalServerError, "500 internal server error")
		return
	}

	thumbnail := getAccountThumbnail(account)
	pngData, err := encodeQrCode(encodedLnUrl, thumbnail, int(size), false)
	if err != nil {
		log.Println("Error creating QR code:", err)
		context.String(http.StatusInternalServerError, "500 internal server error")
		return
	}

	context.Data(http.StatusOK, "image/png", pngData)
}

func lnStaticFileHandler(context *gin.Context) {
	filePath := path.Join("files/static", context.Param("filepath"))
	context.FileFromFS(filePath, http.FS(staticFs))
}

func lnAccountsHandler(context *gin.Context) {
	var accountKeys []string
	for accountKey := range config.Accounts {
		if config.isUserAuthorized(context, accountKey) {
			accountKeys = append(accountKeys, accountKey)
		}
	}
	sort.Strings(accountKeys)

	context.HTML(http.StatusOK, "accounts.gohtml", accountKeys)
}

func lnAccountHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}
	if !config.isUserAuthorized(context, accountKey) {
		context.String(http.StatusNotFound, "404 page not found")
		return
	}

	paymentHashes := repository.loadPaymentHashes(accountKey)

	var invoicesSettled int
	var totalSatsReceived int64
	var comments []LnAccountComment
	for _, paymentHash := range paymentHashes {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.isSettled() {
			invoicesSettled++
			totalSatsReceived += invoice.amount
			if invoice.memo != "" {
				comments = append(comments, LnAccountComment{
					Amount:     invoice.amount,
					SettleDate: invoice.settleDate,
					Comment:    invoice.memo,
				})
			}
		}
	}

	var lnAccountRaffle *LnAccountRaffle
	if raffle := account.Raffle; raffle != nil {
		lnAccountRaffle = &LnAccountRaffle{
			TicketPrice: raffle.TicketPrice,
			PrizesCount: len(raffle.getPrizes()),
		}
	} else {
		sort.Slice(comments, func(i, j int) bool {
			return comments[i].SettleDate.After(comments[j].SettleDate)
		})
	}

	context.HTML(http.StatusOK, "account.gohtml", LnAccount{
		AccountKey:        accountKey,
		Currency:          account.getCurrency(),
		InvoicesIssued:    len(paymentHashes),
		InvoicesSettled:   invoicesSettled,
		TotalSatsReceived: totalSatsReceived,
		TotalFiatReceived: ratesService.satsToFiat(account.getCurrency(), totalSatsReceived),
		Archivable:        account.Archivable && invoicesSettled > 0,
		Raffle:            lnAccountRaffle,
		Comments:          comments,
	})
}

func lnAccountRaffleHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}
	if !config.isUserAuthorized(context, accountKey) || account.Raffle == nil {
		context.String(http.StatusNotFound, "404 page not found")
		return
	}

	paymentHashes := repository.loadPaymentHashes(accountKey)
	rand.Shuffle(len(paymentHashes), func(i, j int) {
		paymentHashes[i], paymentHashes[j] = paymentHashes[j], paymentHashes[i]
	})

	var drawnTickets []LnRaffleTicket
	for _, paymentHash := range paymentHashes {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.isSettled() {
			paymentHash := invoice.getPaymentHash()
			drawnTickets = append(drawnTickets, LnRaffleTicket{
				Number:      invoice.getTicketNumber(),
				PaymentHash: paymentHash[0:5] + "…" + paymentHash[59:],
			})
		}
	}

	if len(drawnTickets) == 0 {
		context.String(http.StatusForbidden, "403 forbidden")
		return
	}

	context.HTML(http.StatusOK, "raffle.gohtml", LnRaffle{
		Prizes:       account.Raffle.getPrizes(),
		DrawnTickets: drawnTickets,
	})
}

func lnAccountTerminalHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}
	if !config.isUserAuthorized(context, accountKey) || account.Raffle != nil {
		context.String(http.StatusNotFound, "404 page not found")
		return
	}

	context.HTML(http.StatusOK, "terminal.gohtml", LnTerminal{
		AccountKey: accountKey,
		Currency:   account.getCurrency(),
		Title:      account.Description,
	})
}

func lnAccountArchiveHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}
	if !config.isUserAuthorized(context, accountKey) || !account.Archivable {
		context.String(http.StatusNotFound, "404 page not found")
		return
	}

	err := repository.archiveStorageFile(accountKey)
	if err != nil {
		log.Println("Error archiving storage file:", err)
		context.String(http.StatusInternalServerError, "500 internal server error")
		return
	}

	context.Status(http.StatusNoContent)
}

func lnInvoicesHandler(context *gin.Context) {
	var createRequest LnCreateInvoice
	if err := context.BindJSON(&createRequest); err != nil {
		context.String(http.StatusBadRequest, "400 bad request")
		return
	}

	accountKey := createRequest.AccountKey
	account, accountExists := config.Accounts[accountKey]
	if !accountExists || !config.isUserAuthorized(context, accountKey) {
		context.String(http.StatusBadRequest, "400 bad request")
		return
	}

	amount, err := strconv.ParseFloat(createRequest.Amount, 32)
	if err != nil || amount <= 0 || amount >= 1_000_000 {
		log.Println("Invalid amount:", amount)
		context.String(http.StatusBadRequest, "400 bad request")
		return
	}

	msats := msats(ratesService.fiatToSats(account.getCurrency(), amount))
	invoice := createInvoice(context, accountKey, msats, "", []byte{})
	if invoice == nil {
		return
	}

	thumbnail := getAccountThumbnail(&account)
	pngData, err := encodeQrCode(strings.ToUpper(invoice.paymentRequest), thumbnail, 1280, true)
	if err != nil {
		log.Println("Error creating QR code:", err)
		context.String(http.StatusInternalServerError, "500 internal server error")
		return
	}

	context.JSON(http.StatusOK, LnInvoice{
		PaymentHash: invoice.getPaymentHash(),
		QrCode:      "image/png;base64," + base64.StdEncoding.EncodeToString(pngData),
	})
}

func lnInvoiceStatusHandler(context *gin.Context) {
	paymentHash := context.Param("paymentHash")
	invoice, err := lndClient.getInvoice(paymentHash)
	if err != nil {
		context.String(http.StatusNotFound, "404 page not found")
		return
	}

	context.JSON(http.StatusOK, LnInvoiceStatus{
		Settled: invoice.isSettled(),
	})
}

func getAccount(context *gin.Context) (string, *Account) {
	accountKey := context.Param("name")
	if account, accountExists := config.Accounts[accountKey]; accountExists {
		return accountKey, &account
	}

	context.String(http.StatusNotFound, "404 page not found")
	return "", nil
}

func getSchemeAndHost(context *gin.Context) (string, string) {
	scheme := "http"
	host := context.Request.Host

	header := context.Request.Header
	if forwardedProto := header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	}
	if forwardedHost := header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}

	return scheme, host
}

func getAccountThumbnail(account *Account) *Thumbnail {
	if account.Thumbnail == "" {
		return nil
	}

	thumbnail, err := repository.loadThumbnail(account.Thumbnail)
	if err != nil {
		log.Println("Thumbnail not readable:", err)
	}

	return thumbnail
}

func createInvoice(context *gin.Context, accountKey string, msats int64, comment string, descriptionHash []byte) *Invoice {
	if msats == 0 {
		log.Println("Zero invoice requested")
		context.JSON(http.StatusInternalServerError, lnurl.ErrorResponse("Internal server error"))
		return nil
	}

	invoice, err := lndClient.createInvoice(msats, comment, descriptionHash)
	if err != nil {
		log.Println("Error creating invoice:", err)
		context.JSON(http.StatusInternalServerError, lnurl.ErrorResponse("Error creating invoice"))
		return nil
	}
	if err := repository.storePaymentHash(accountKey, invoice.getPaymentHash()); err != nil {
		log.Println("Error storing payment hash:", err)
		context.JSON(http.StatusInternalServerError, lnurl.ErrorResponse("Error storing payment hash"))
		return nil
	}

	return invoice
}
