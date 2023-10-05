package main

import (
	"crypto/sha256"
	"embed"
	"errors"
	"flag"
	"fmt"
	"github.com/fiatjaf/go-lnurl"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"golang.org/x/exp/slices"
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

type LnAuthInit struct {
	K1     string `json:"k1"`
	LnUrl  string `json:"lnUrl"`
	QrCode string `json:"qrCode"`
}

type LnAuthIdentity struct {
	Identity string `json:"identity"`
}

type LnEvent struct {
	EventKey    string
	Title       string
	DateTime    time.Time
	Location    string
	Capacity    uint16
	Description string
	Attendees   int
	Attending   bool
	IdentityId  string
}

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
	Prizes       []LnRafflePrize
	DrawnTickets []LnRaffleTicket
}

type LnRafflePrize struct {
	Name     string `json:"name"`
	Quantity uint8  `json:"quantity"`
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
	//go:embed files/lightning.png
	lightningPngData []byte

	config       *Config
	repository   *Repository
	lndClient    *LndClient
	authService  *AuthService
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
	authService = newAuthService()
	ratesService = newRatesService(21 * time.Second)

	lnurld := gin.Default()
	_ = lnurld.SetTrustedProxies(nil)
	loadTemplates(lnurld, "files/templates/*.gohtml")

	sessionStore := cookie.NewStore(config.getCookieKey())
	lnurld.Use(sessions.Sessions("lnSession", sessionStore))
	lnurld.NoRoute(abortWithNotFoundResponse)

	lnurld.POST("/ln/auth", lnAuthInitHandler)
	lnurld.GET("/ln/auth", lnAuthVerifyHandler)
	lnurld.GET("/ln/auth/:k1", lnAuthIdentityHandler)
	lnurld.GET("/.well-known/lnurlp/:name", lnPayHandler)
	lnurld.GET("/ln/pay/:name", lnPayHandler)
	lnurld.GET("/ln/pay/:name/qr-code", lnPayQrCodeHandler)
	lnurld.GET("/ln/events/:name", lnEventHandler)
	lnurld.POST("/ln/events/:name/sign-up", lnEventSignUpHandler)
	lnurld.GET("/ln/static/*filepath", lnStaticFileHandler)

	authorized := lnurld.Group("/", gin.BasicAuth(config.Credentials))
	authorized.GET("/ln/accounts", lnAccountsHandler)
	authorized.GET("/ln/accounts/:name", lnAccountHandler)
	authorized.GET("/ln/accounts/:name/raffle", lnAccountRaffleHandler)
	authorized.GET("/ln/accounts/:name/terminal", lnAccountTerminalHandler)
	authorized.POST("/ln/accounts/:name/archive", lnAccountArchiveHandler)
	authorized.POST("/ln/invoices", lnInvoicesHandler)
	authorized.GET("/ln/invoices/:paymentHash", lnInvoiceStatusHandler)

	log.Fatal(lnurld.Run(config.Listen))
}

func lnAuthInitHandler(context *gin.Context) {
	k1 := authService.init()

	scheme, host := getSchemeAndHost(context)
	actualLnUrl := scheme + "://" + host + "/ln/auth?tag=login&k1=" + k1
	encodedLnUrl, err := lnurl.LNURLEncode(actualLnUrl)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding LNURL: %w", err))
		return
	}

	pngData, err := encodeQrCode(encodedLnUrl, lightningPngData, 1280, true)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.JSON(http.StatusOK, LnAuthInit{
		K1:     k1,
		LnUrl:  encodedLnUrl,
		QrCode: pngDataUrl(pngData),
	})
}

func lnAuthVerifyHandler(context *gin.Context) {
	k1, sig, key := context.Query("k1"), context.Query("sig"), context.Query("key")
	if err := authService.verify(k1, sig, key); err != nil {
		context.Error(fmt.Errorf("authentication failed: %w", err))
		abortWithBadRequestResponse(context, "invalid request")
		return
	}

	context.JSON(http.StatusOK, lnurl.OkResponse())
}

func lnAuthIdentityHandler(context *gin.Context) {
	identity := authService.identity(context.Param("k1"))
	if identity == "" {
		abortWithNotFoundResponse(context)
		return
	}

	session := sessions.Default(context)
	session.Set("identity", identity)
	if err := session.Save(); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing session: %w", err))
		return
	}

	context.JSON(http.StatusOK, LnAuthIdentity{identity})
}

func lnEventHandler(context *gin.Context) {
	eventKey, event := getEvent(context)
	if eventKey == "" {
		return
	}

	identities := repository.loadIdentities(eventKey)
	identity := getIdentity(context)

	context.HTML(http.StatusOK, "event.gohtml", LnEvent{
		EventKey:    eventKey,
		Title:       event.Title,
		DateTime:    event.DateTime,
		Location:    event.Location,
		Capacity:    event.Capacity,
		Description: event.Description,
		Attendees:   len(identities),
		Attending:   slices.Contains(identities, identity),
		IdentityId:  toIdentityId(identity),
	})
}

func lnEventSignUpHandler(context *gin.Context) {
	eventKey, _ := getEvent(context)
	if eventKey == "" {
		return
	}

	identity := getIdentity(context)
	if identity == "" {
		abortWithForbiddenResponse(context, "authentication required")
		return
	}

	identities := repository.loadIdentities(eventKey)
	if !slices.Contains(identities, identity) {
		if err := repository.storeIdentity(eventKey, identity); err != nil {
			abortWithInternalServerErrorResponse(context, fmt.Errorf("storing identity: %w", err))
			return
		}
	}

	context.Status(http.StatusNoContent)
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

	if thumbnail := getAccountThumbnail(account); thumbnail != nil {
		lnurlMetadata.Image.Bytes = thumbnail.bytes
		lnurlMetadata.Image.Ext = thumbnail.ext
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
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	comment := context.Query("comment")
	if commentLength := len(comment); commentLength > int(account.CommentAllowed) {
		abortWithBadRequestResponse(context, "invalid comment length")
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
		abortWithBadRequestResponse(context, "invalid size")
		return
	}

	scheme, host := getSchemeAndHost(context)
	actualLnUrl := scheme + "://" + host + "/ln/pay/" + accountKey
	encodedLnUrl, err := lnurl.LNURLEncode(actualLnUrl)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding LNURL: %w", err))
		return
	}

	thumbnailData := getAccountThumbnailData(account)
	pngData, err := encodeQrCode(encodedLnUrl, thumbnailData, int(size), false)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
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
		abortWithNotFoundResponse(context)
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
			PrizesCount: raffle.getPrizesCount(),
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
		abortWithNotFoundResponse(context)
		return
	}

	paymentHashes := repository.loadPaymentHashes(accountKey)
	rand.Shuffle(len(paymentHashes), func(i, j int) {
		paymentHashes[i], paymentHashes[j] = paymentHashes[j], paymentHashes[i]
	})

	var prizes []LnRafflePrize
	for _, entry := range account.Raffle.Prizes {
		for prize, quantity := range entry {
			prizes = append(prizes, LnRafflePrize{
				Name:     prize,
				Quantity: quantity,
			})
		}
	}

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

	if len(drawnTickets) < account.Raffle.getPrizesCount() {
		abortWithForbiddenResponse(context, "not enough tickets")
		return
	}

	context.HTML(http.StatusOK, "raffle.gohtml", LnRaffle{
		Prizes:       prizes,
		DrawnTickets: drawnTickets,
	})
}

func lnAccountTerminalHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}
	if !config.isUserAuthorized(context, accountKey) || account.Raffle != nil {
		abortWithNotFoundResponse(context)
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
		abortWithNotFoundResponse(context)
		return
	}

	err := repository.archiveStorageFile(accountKey)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("archiving storage file: %w", err))
		return
	}

	context.Status(http.StatusNoContent)
}

func lnInvoicesHandler(context *gin.Context) {
	var createRequest LnCreateInvoice
	if err := context.BindJSON(&createRequest); err != nil {
		abortWithBadRequestResponse(context, "invalid request")
		return
	}

	accountKey := createRequest.AccountKey
	account, accountExists := config.Accounts[accountKey]
	if !accountExists || !config.isUserAuthorized(context, accountKey) {
		abortWithBadRequestResponse(context, "invalid accountKey")
		return
	}

	amount, err := strconv.ParseFloat(createRequest.Amount, 32)
	if err != nil || amount <= 0 || amount >= 1_000_000 {
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	msats := msats(ratesService.fiatToSats(account.getCurrency(), amount))
	invoice := createInvoice(context, accountKey, msats, "", []byte{})
	if invoice == nil {
		return
	}

	thumbnailData := getAccountThumbnailData(&account)
	pngData, err := encodeQrCode(strings.ToUpper(invoice.paymentRequest), thumbnailData, 1280, true)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.JSON(http.StatusOK, LnInvoice{
		PaymentHash: invoice.getPaymentHash(),
		QrCode:      pngDataUrl(pngData),
	})
}

func lnInvoiceStatusHandler(context *gin.Context) {
	paymentHash := context.Param("paymentHash")
	invoice, err := lndClient.getInvoice(paymentHash)
	if err != nil {
		abortWithNotFoundResponse(context)
		return
	}

	context.JSON(http.StatusOK, LnInvoiceStatus{
		Settled: invoice.isSettled(),
	})
}

func getIdentity(context *gin.Context) string {
	session := sessions.Default(context)
	if identity := session.Get("identity"); identity != nil {
		return identity.(string)
	}
	return ""
}

func getEvent(context *gin.Context) (string, *Event) {
	eventKey := context.Param("name")
	if event, eventExists := config.Events[eventKey]; eventExists {
		return eventKey, &event
	}

	abortWithNotFoundResponse(context)
	return "", nil
}

func getAccount(context *gin.Context) (string, *Account) {
	accountKey := context.Param("name")
	if account, accountExists := config.Accounts[accountKey]; accountExists {
		return accountKey, &account
	}

	abortWithNotFoundResponse(context)
	return "", nil
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

func getAccountThumbnailData(account *Account) []byte {
	if thumbnail := getAccountThumbnail(account); thumbnail != nil {
		return thumbnail.bytes
	}
	return lightningPngData
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

func createInvoice(context *gin.Context, accountKey string, msats int64, comment string, descriptionHash []byte) *Invoice {
	if msats == 0 {
		abortWithInternalServerErrorResponse(context, errors.New("zero invoice requested"))
		return nil
	}

	invoice, err := lndClient.createInvoice(msats, comment, descriptionHash)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("creating invoice: %w", err))
		return nil
	}
	if err := repository.storePaymentHash(accountKey, invoice.getPaymentHash()); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing payment hash: %w", err))
		return nil
	}

	return invoice
}

func abortWithNotFoundResponse(context *gin.Context) {
	context.AbortWithStatusJSON(http.StatusNotFound, lnurl.ErrorResponse("not found"))
}

func abortWithForbiddenResponse(context *gin.Context, reason string) {
	context.AbortWithStatusJSON(http.StatusForbidden, lnurl.ErrorResponse(reason))
}

func abortWithBadRequestResponse(context *gin.Context, reason string) {
	context.AbortWithStatusJSON(http.StatusBadRequest, lnurl.ErrorResponse(reason))
}

func abortWithInternalServerErrorResponse(context *gin.Context, err error) {
	context.AbortWithStatusJSON(http.StatusInternalServerError, lnurl.ErrorResponse("internal server error"))
	context.Error(err)
}
