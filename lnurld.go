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

type AccountComment struct {
	Amount     int64
	SettleDate time.Time
	Comment    string
}

type InvoiceRequest struct {
	AccountKey string `json:"accountKey"`
	Amount     string `json:"amount"`
}

type InvoiceResponse struct {
	PaymentHash string `json:"paymentHash"`
	QrCode      string `json:"qrCode"`
}

type InvoiceStatus struct {
	Settled bool `json:"settled"`
}

type Event struct {
	Id          string        `json:"-"`
	Owner       string        `json:"owner"`
	Title       string        `json:"title" binding:"min=1,max=50"`
	DateTime    time.Time     `json:"dateTime" binding:"required"`
	Location    EventLocation `json:"location" binding:"required"`
	Capacity    uint16        `json:"capacity" binding:"min=1,max=1000"`
	Description string        `json:"description" binding:"min=1,max=500"`
}

type EventLocation struct {
	Name string `json:"name" binding:"min=1,max=50"`
	Url  string `json:"url" binding:"url,max=100"`
}

type Raffle struct {
	Id           string        `json:"-"`
	Owner        string        `json:"owner"`
	Title        string        `json:"title" binding:"min=1,max=50"`
	TicketPrice  uint32        `json:"ticketPrice" binding:"min=1,max=1000000"`
	FiatCurrency Currency      `json:"fiatCurrency" binding:"required"`
	Prizes       []RafflePrize `json:"prizes" binding:"min=1,max=10"`
}

func (raffle *Raffle) getPrizesCount() int {
	var prizesCount int
	for _, prize := range raffle.Prizes {
		prizesCount += int(prize.Quantity)
	}
	return prizesCount
}

type RafflePrize struct {
	Name     string `json:"name" binding:"min=1,max=50"`
	Quantity uint8  `json:"quantity" binding:"min=1,max=10"`
}

type RaffleTicket struct {
	Number      string `json:"number"`
	PaymentHash string `json:"paymentHash"`
}

const (
	payRequestTag = "payRequest"
	qrCodeSize    = 1280
)

var (
	//go:embed files/static
	staticFs embed.FS
	//go:embed files/templates
	templatesFs embed.FS
	//go:embed files/lightning.png
	lightningPngData []byte
	//go:embed files/tombola.png
	tombolaPngData []byte

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

	lnurld.GET("/", indexHandler)
	lnurld.POST("/ln/auth", lnAuthInitHandler)
	lnurld.GET("/ln/auth", lnAuthVerifyHandler)
	lnurld.GET("/ln/auth/:k1", lnAuthIdentityHandler)
	lnurld.GET("/.well-known/lnurlp/:name", lnPayHandler)
	lnurld.GET("/ln/pay/:name", lnPayHandler)
	lnurld.GET("/ln/pay/:name/qr-code", lnPayQrCodeHandler)
	lnurld.GET("/ln/raffle/:id", lnRaffleTicketHandler)
	lnurld.GET("/ln/raffle/:id/qr-code", lnRaffleQrCodeHandler)
	lnurld.GET("/events/:id", eventHandler)
	lnurld.POST("/events/:id/sign-up", eventSignUpHandler)
	lnurld.GET("/static/*filepath", lnStaticFileHandler)

	authorized := lnurld.Group("/", gin.BasicAuth(config.Credentials))
	authorized.GET("/auth", authHomeHandler)
	authorized.GET("/auth/accounts", authAccountsHandler)
	authorized.GET("/auth/accounts/:name", authAccountHandler)
	authorized.GET("/auth/accounts/:name/terminal", authAccountTerminalHandler)
	authorized.GET("/auth/events", authEventsHandler)
	authorized.GET("/auth/raffles", authRafflesHandler)
	authorized.GET("/auth/raffles/:id", authRaffleHandler)
	authorized.GET("/auth/raffles/:id/draw", authRaffleDrawHandler)
	authorized.POST("/api/accounts/:name/archive", apiAccountArchiveHandler)
	authorized.POST("/api/invoices", apiInvoicesHandler)
	authorized.GET("/api/invoices/:paymentHash", apiInvoiceStatusHandler)
	authorized.POST("/api/events", apiEventCreateHandler)
	authorized.GET("/api/events/:id", apiEventReadHandler)
	authorized.PUT("/api/events/:id", apiEventUpdateHandler)
	authorized.POST("/api/raffles", apiRaffleCreateHandler)
	authorized.GET("/api/raffles/:id", apiRaffleReadHandler)
	authorized.PUT("/api/raffles/:id", apiRaffleUpdateHandler)
	authorized.POST("/api/raffles/:id/archive", apiRaffleArchiveHandler)

	log.Fatal(lnurld.Run(config.Listen))
}

func indexHandler(context *gin.Context) {
	context.HTML(http.StatusOK, "index.gohtml", gin.H{})
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

	pngData, err := encodeQrCode(encodedLnUrl, lightningPngData, qrCodeSize, true)
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
			Tag:             payRequestTag,
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
	invoice := createInvoice(context, msats, comment, metadataHash[:])
	if invoice == nil {
		return
	}
	if err := repository.addAccountPaymentHash(accountKey, invoice.getPaymentHash()); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing payment hash: %w", err))
		return
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: lnurl.Action("Thanks, payment received!", ""),
		Routes:        []string{},
	})
}

func lnPayQrCodeHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}

	scheme, host := getSchemeAndHost(context)
	lnUrl := scheme + "://" + host + "/ln/pay/" + accountKey
	thumbnailData := getAccountThumbnailData(account)

	generateQrCode(context, lnUrl, thumbnailData)
}

func lnRaffleTicketHandler(context *gin.Context) {
	raffle := getRaffle(context)
	if raffle == nil {
		return
	}

	ticketPrice := msats(raffle.TicketPrice)

	var lnurlMetadata lnurl.Metadata
	lnurlMetadata.Description = raffle.Title
	lnurlMetadata.Image.Bytes = tombolaPngData
	lnurlMetadata.Image.Ext = "png"

	amount := context.Query("amount")
	if amount == "" {
		scheme, host := getSchemeAndHost(context)
		context.JSON(http.StatusOK, lnurl.LNURLPayParams{
			Callback:        scheme + "://" + host + context.Request.RequestURI,
			MaxSendable:     ticketPrice,
			MinSendable:     ticketPrice,
			EncodedMetadata: lnurlMetadata.Encode(),
			Tag:             payRequestTag,
		})
		return
	}

	msats, err := strconv.ParseInt(amount, 10, 64)
	if err != nil || msats != ticketPrice {
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	if len(context.Query("comment")) > 0 {
		abortWithBadRequestResponse(context, "comment not supported")
		return
	}

	metadataHash := sha256.Sum256([]byte(lnurlMetadata.Encode()))
	invoice := createInvoice(context, msats, "", metadataHash[:])
	if invoice == nil {
		return
	}
	if err := repository.addRaffleTicket(raffle, invoice.getPaymentHash()); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing payment hash: %w", err))
		return
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: lnurl.Action("Ticket "+invoice.getTicketNumber(), ""),
		Routes:        []string{},
	})
}

func lnRaffleQrCodeHandler(context *gin.Context) {
	raffle := getRaffle(context)
	if raffle == nil {
		return
	}

	scheme, host := getSchemeAndHost(context)
	lnUrl := scheme + "://" + host + "/ln/raffle/" + raffle.Id

	generateQrCode(context, lnUrl, tombolaPngData)
}

func eventHandler(context *gin.Context) {
	event := getEvent(context)
	if event == nil {
		return
	}

	attendees := repository.getEventAttendees(event)
	identity := getIdentity(context)

	context.HTML(http.StatusOK, "event.gohtml", gin.H{
		"Id":              event.Id,
		"Title":           event.Title,
		"DateTime":        event.DateTime,
		"Location":        event.Location,
		"Capacity":        event.Capacity,
		"Description":     event.Description,
		"Attendees":       len(attendees),
		"AttendeeOrdinal": slices.Index(attendees, identity) + 1,
		"SignUpPossible":  event.DateTime.After(time.Now()),
		"IdentityId":      toIdentityId(identity),
	})
}

func eventSignUpHandler(context *gin.Context) {
	event := getEvent(context)
	if event == nil {
		return
	}
	if event.DateTime.Before(time.Now()) {
		abortWithBadRequestResponse(context, "already started")
		return
	}

	identity := getIdentity(context)
	if identity == "" {
		abortWithUnauthorizedResponse(context)
		return
	}

	attendees := repository.getEventAttendees(event)
	if !slices.Contains(attendees, identity) {
		if err := repository.addEventAttendee(event, identity); err != nil {
			abortWithInternalServerErrorResponse(context, fmt.Errorf("storing attendee: %w", err))
			return
		}
	}

	context.Status(http.StatusNoContent)
}

func lnStaticFileHandler(context *gin.Context) {
	filePath := path.Join("files/static", context.Param("filepath"))
	context.FileFromFS(filePath, http.FS(staticFs))
}

func authHomeHandler(context *gin.Context) {
	accountKeys := getAccessibleAccountKeys(context)

	context.HTML(http.StatusOK, "auth.gohtml", accountKeys)
}

func authAccountsHandler(context *gin.Context) {
	accountKeys := getAccessibleAccountKeys(context)
	sort.Strings(accountKeys)

	context.HTML(http.StatusOK, "accounts.gohtml", accountKeys)
}

func authAccountHandler(context *gin.Context) {
	accountKey, account := getAccessibleAccount(context)
	if accountKey == "" {
		return
	}

	paymentHashes := repository.getAccountPaymentHashes(accountKey)

	var invoicesSettled int
	var totalSatsReceived int64
	var comments []AccountComment
	for _, paymentHash := range paymentHashes {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.isSettled() {
			invoicesSettled++
			totalSatsReceived += invoice.amount
			if invoice.memo != "" {
				comments = append(comments, AccountComment{
					Amount:     invoice.amount,
					SettleDate: invoice.settleDate,
					Comment:    invoice.memo,
				})
			}
		}
	}

	sort.Slice(comments, func(i, j int) bool {
		return comments[i].SettleDate.After(comments[j].SettleDate)
	})

	context.HTML(http.StatusOK, "account.gohtml", gin.H{
		"AccountKey":        accountKey,
		"FiatCurrency":      account.getCurrency(),
		"InvoicesIssued":    len(paymentHashes),
		"InvoicesSettled":   invoicesSettled,
		"TotalSatsReceived": totalSatsReceived,
		"TotalFiatReceived": ratesService.satsToFiat(account.getCurrency(), totalSatsReceived),
		"Archivable":        account.Archivable && invoicesSettled > 0,
		"Comments":          comments,
	})
}

func authAccountTerminalHandler(context *gin.Context) {
	accountKey, account := getAccessibleAccount(context)
	if accountKey == "" {
		return
	}

	context.HTML(http.StatusOK, "terminal.gohtml", gin.H{
		"AccountKey": accountKey,
		"Currency":   account.getCurrency(),
		"Title":      account.Description,
	})
}

func authEventsHandler(context *gin.Context) {
	var events []*Event
	for _, event := range repository.getEvents() {
		if isUserAuthorized(context, event.Owner) {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].DateTime.Before(events[j].DateTime)
	})

	context.HTML(http.StatusOK, "events.gohtml", gin.H{
		"Events":         events,
		"TimeZoneOffset": time.Now().Format("-07:00"),
	})
}

func authRafflesHandler(context *gin.Context) {
	var raffles []*Raffle
	for _, raffle := range repository.getRaffles() {
		if isUserAuthorized(context, raffle.Owner) {
			raffles = append(raffles, raffle)
		}
	}
	sort.Slice(raffles, func(i, j int) bool {
		return raffles[i].Title < raffles[j].Title
	})

	context.HTML(http.StatusOK, "raffles.gohtml", gin.H{
		"Raffles":        raffles,
		"FiatCurrencies": supportedCurrencies(),
	})
}

func authRaffleHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	tickets := repository.getRaffleTickets(raffle)

	var ticketsPaid int
	var totalSatsReceived int64
	for _, paymentHash := range tickets {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.isSettled() {
			ticketsPaid++
			totalSatsReceived += invoice.amount
		}
	}

	context.HTML(http.StatusOK, "raffle.gohtml", gin.H{
		"Id":                raffle.Id,
		"Title":             raffle.Title,
		"TicketPrice":       raffle.TicketPrice,
		"FiatCurrency":      raffle.FiatCurrency,
		"PrizesCount":       raffle.getPrizesCount(),
		"TicketsIssued":     len(tickets),
		"TicketsPaid":       ticketsPaid,
		"TotalSatsReceived": totalSatsReceived,
		"TotalFiatReceived": ratesService.satsToFiat(raffle.FiatCurrency, totalSatsReceived),
		"Archivable":        isAdministrator(context) && len(tickets) > 0,
	})
}

func authRaffleDrawHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	tickets := repository.getRaffleTickets(raffle)
	rand.Shuffle(len(tickets), func(i, j int) {
		tickets[i], tickets[j] = tickets[j], tickets[i]
	})

	var drawnTickets []RaffleTicket
	for _, paymentHash := range tickets {
		invoice, _ := lndClient.getInvoice(paymentHash)
		if invoice != nil && invoice.isSettled() {
			drawnTickets = append(drawnTickets, RaffleTicket{
				Number:      invoice.getTicketNumber(),
				PaymentHash: paymentHash[0:5] + "…" + paymentHash[59:],
			})
		}
	}

	if len(drawnTickets) < raffle.getPrizesCount() {
		abortWithBadRequestResponse(context, "not enough tickets")
		return
	}

	context.HTML(http.StatusOK, "draw.gohtml", gin.H{
		"Title":        raffle.Title,
		"Prizes":       raffle.Prizes,
		"DrawnTickets": drawnTickets,
	})
}

func apiAccountArchiveHandler(context *gin.Context) {
	accountKey, account := getAccessibleAccount(context)
	if accountKey == "" {
		return
	}
	if !account.Archivable {
		abortWithBadRequestResponse(context, "not archivable")
		return
	}

	err := repository.archiveAccountPaymentHashes(accountKey)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("archiving storage file: %w", err))
		return
	}

	context.Status(http.StatusNoContent)
}

func apiInvoicesHandler(context *gin.Context) {
	var request InvoiceRequest
	if err := context.BindJSON(&request); err != nil {
		abortWithBadRequestResponse(context, "invalid request")
		return
	}

	accountKey := request.AccountKey
	account, accountExists := config.Accounts[accountKey]
	if !accountExists || !isAccountAccessible(context, accountKey) {
		abortWithBadRequestResponse(context, "invalid accountKey")
		return
	}

	amount, err := strconv.ParseFloat(request.Amount, 32)
	if err != nil || amount <= 0 || amount >= 1_000_000 {
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	msats := msats(ratesService.fiatToSats(account.getCurrency(), amount))
	invoice := createInvoice(context, msats, "", []byte{})
	if invoice == nil {
		return
	}
	if err := repository.addAccountPaymentHash(accountKey, invoice.getPaymentHash()); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing payment hash: %w", err))
		return
	}

	thumbnailData := getAccountThumbnailData(&account)
	pngData, err := encodeQrCode(strings.ToUpper(invoice.paymentRequest), thumbnailData, qrCodeSize, true)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.JSON(http.StatusOK, InvoiceResponse{
		PaymentHash: invoice.getPaymentHash(),
		QrCode:      pngDataUrl(pngData),
	})
}

func apiInvoiceStatusHandler(context *gin.Context) {
	paymentHash := context.Param("paymentHash")
	invoice, err := lndClient.getInvoice(paymentHash)
	if err != nil {
		abortWithNotFoundResponse(context)
		return
	}

	context.JSON(http.StatusOK, InvoiceStatus{
		Settled: invoice.isSettled(),
	})
}

func apiEventCreateHandler(context *gin.Context) {
	var event Event
	if err := context.BindJSON(&event); err != nil {
		abortWithBadRequestResponse(context, err.Error())
		return
	}
	event.Owner = context.GetString(gin.AuthUserKey)

	err := repository.createEvent(&event)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("creating event: %w", err))
		return
	}

	context.JSON(http.StatusCreated, event)
}

func apiEventReadHandler(context *gin.Context) {
	event := getAccessibleEvent(context)
	if event == nil {
		return
	}

	context.JSON(http.StatusOK, event)
}

func apiEventUpdateHandler(context *gin.Context) {
	event := getAccessibleEvent(context)
	if event == nil {
		return
	}

	var updatedEvent Event
	if err := context.BindJSON(&updatedEvent); err != nil {
		abortWithBadRequestResponse(context, err.Error())
		return
	}
	updatedEvent.Id = event.Id
	updatedEvent.Owner = event.Owner

	err := repository.updateEvent(&updatedEvent)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("updating event: %w", err))
		return
	}

	context.JSON(http.StatusOK, updatedEvent)
}

func apiRaffleCreateHandler(context *gin.Context) {
	var raffle Raffle
	if err := context.BindJSON(&raffle); err != nil {
		abortWithBadRequestResponse(context, err.Error())
		return
	}
	raffle.Owner = context.GetString(gin.AuthUserKey)

	err := repository.createRaffle(&raffle)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("creating raffle: %w", err))
		return
	}

	context.JSON(http.StatusCreated, raffle)
}

func apiRaffleReadHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	context.JSON(http.StatusOK, raffle)
}

func apiRaffleUpdateHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	var updatedRaffle Raffle
	if err := context.BindJSON(&updatedRaffle); err != nil {
		abortWithBadRequestResponse(context, err.Error())
		return
	}
	updatedRaffle.Id = raffle.Id
	updatedRaffle.Owner = raffle.Owner

	err := repository.updateRaffle(&updatedRaffle)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("updating raffle: %w", err))
		return
	}

	context.JSON(http.StatusOK, updatedRaffle)
}

func apiRaffleArchiveHandler(context *gin.Context) {
	if !isAdministrator(context) {
		abortWithNotFoundResponse(context)
		return
	}

	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	err := repository.archiveRaffleTickets(raffle)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("archiving tickets file: %w", err))
		return
	}

	context.Status(http.StatusNoContent)
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

func getIdentity(context *gin.Context) string {
	session := sessions.Default(context)
	if identity := session.Get("identity"); identity != nil {
		return identity.(string)
	}
	return ""
}

func getAccount(context *gin.Context) (string, *Account) {
	accountKey := context.Param("name")
	if account, accountExists := config.Accounts[accountKey]; accountExists {
		return accountKey, &account
	}

	abortWithNotFoundResponse(context)
	return "", nil
}

func getAccessibleAccount(context *gin.Context) (string, *Account) {
	accountKey, account := getAccount(context)
	if account == nil || isAccountAccessible(context, accountKey) {
		return accountKey, account
	}

	abortWithNotFoundResponse(context)
	return "", nil
}

func getAccessibleAccountKeys(context *gin.Context) []string {
	var accountKeys []string
	for accountKey := range config.Accounts {
		if isAccountAccessible(context, accountKey) {
			accountKeys = append(accountKeys, accountKey)
		}
	}

	return accountKeys
}

func isAccountAccessible(context *gin.Context, accountKey string) bool {
	user := context.GetString(gin.AuthUserKey)
	if slices.Contains(config.Administrators, user) {
		return true
	}
	return slices.Contains(config.AccessControl[user], accountKey)
}

func getAccountThumbnail(account *Account) *Thumbnail {
	if account.Thumbnail == "" {
		return nil
	}

	thumbnail, err := repository.getThumbnail(account.Thumbnail)
	if err != nil {
		log.Println("thumbnail not readable:", err)
	}

	return thumbnail
}

func getAccountThumbnailData(account *Account) []byte {
	if thumbnail := getAccountThumbnail(account); thumbnail != nil {
		return thumbnail.bytes
	}
	return lightningPngData
}

func getEvent(context *gin.Context) *Event {
	eventId := context.Param("id")
	if event := repository.getEvent(eventId); event != nil {
		return event
	}

	abortWithNotFoundResponse(context)
	return nil
}

func getAccessibleEvent(context *gin.Context) *Event {
	event := getEvent(context)
	if event == nil || isUserAuthorized(context, event.Owner) {
		return event
	}

	abortWithNotFoundResponse(context)
	return nil
}

func getRaffle(context *gin.Context) *Raffle {
	raffleId := context.Param("id")
	if raffle := repository.getRaffle(raffleId); raffle != nil {
		return raffle
	}

	abortWithNotFoundResponse(context)
	return nil
}

func getAccessibleRaffle(context *gin.Context) *Raffle {
	raffle := getRaffle(context)
	if raffle == nil || isUserAuthorized(context, raffle.Owner) {
		return raffle
	}

	abortWithNotFoundResponse(context)
	return nil
}

func isUserAuthorized(context *gin.Context, owner string) bool {
	user := context.GetString(gin.AuthUserKey)
	return user == owner || slices.Contains(config.Administrators, user)
}

func isAdministrator(context *gin.Context) bool {
	user := context.GetString(gin.AuthUserKey)
	return slices.Contains(config.Administrators, user)
}

func createInvoice(context *gin.Context, msats int64, comment string, descriptionHash []byte) *Invoice {
	if msats == 0 {
		abortWithInternalServerErrorResponse(context, errors.New("zero invoice requested"))
		return nil
	}

	invoice, err := lndClient.createInvoice(msats, comment, descriptionHash)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("creating invoice: %w", err))
		return nil
	}

	return invoice
}

func generateQrCode(context *gin.Context, lnUrl string, thumbnailData []byte) {
	encodedLnUrl, err := lnurl.LNURLEncode(lnUrl)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding LNURL: %w", err))
		return
	}

	requestedSize := context.DefaultQuery("size", "256")
	size, err := strconv.ParseUint(requestedSize, 10, 12)
	if err != nil {
		abortWithBadRequestResponse(context, "invalid size")
		return
	}

	pngData, err := encodeQrCode(encodedLnUrl, thumbnailData, int(size), false)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.Data(http.StatusOK, "image/png", pngData)
}

func abortWithNotFoundResponse(context *gin.Context) {
	context.AbortWithStatusJSON(http.StatusNotFound, lnurl.ErrorResponse("not found"))
}

func abortWithUnauthorizedResponse(context *gin.Context) {
	context.AbortWithStatusJSON(http.StatusUnauthorized, lnurl.ErrorResponse("authentication required"))
}

func abortWithBadRequestResponse(context *gin.Context, reason string) {
	context.AbortWithStatusJSON(http.StatusBadRequest, lnurl.ErrorResponse(reason))
}

func abortWithInternalServerErrorResponse(context *gin.Context, err error) {
	context.AbortWithStatusJSON(http.StatusInternalServerError, lnurl.ErrorResponse("internal server error"))
	context.Error(err)
}
