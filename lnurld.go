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
	"github.com/nbd-wtf/go-nostr"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type LnUrlData struct {
	K1     string `json:"k1"`
	LnUrl  string `json:"lnUrl"`
	QrCode string `json:"qrCode"`
}

type LnAuthIdentity struct {
	Identity string `json:"identity"`
}

type AccountSummary struct {
	Description      string
	NewInvoicesCount int
}

type AccountInvoice struct {
	Amount     int64
	SettleDate time.Time
	Comment    string
	IsNew      bool
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

const (
	qrCodeSize = 1280
)

var (
	//go:embed files/static
	staticFs embed.FS
	//go:embed files/lightning.png
	lightningPngData []byte
	//go:embed files/tombola.png
	tombolaPngData []byte

	config                *Config
	repository            *Repository
	lndClient             *LndClient
	authenticationService *AuthenticationService
	withdrawalService     *WithdrawalService
	nostrService          *NostrService
	ratesService          *RatesService
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
	nostrService = newNostrService(config.DataDir, config.Nostr)
	authenticationService = newAuthenticationService(config.Authentication)
	withdrawalService = newWithdrawalService(config.Withdrawal)
	ratesService = newRatesService(30 * time.Second)

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
	lnurld.GET("/ln/withdraw", lnWithdrawConfirmHandler)
	lnurld.GET("/ln/withdraw/:k1", lnWithdrawRequestHandler)
	lnurld.GET("/events/:id", eventHandler)
	lnurld.GET("/events/:id/ics", eventIcsHandler)
	lnurld.POST("/events/:id/sign-up", eventSignUpHandler)
	lnurld.GET("/static/*filepath", lnStaticFileHandler)

	authorized := lnurld.Group("/", gin.BasicAuth(config.Credentials))
	authorized.Use(setCacheControlHeader)
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
	authorized.POST("/api/raffles/:id/withdraw", apiRaffleWithdrawHandler)
	authorized.POST("/api/raffles/:id/lock", apiRaffleLockHandler)

	log.Fatal(lnurld.Run(config.Listen))
}

func indexHandler(context *gin.Context) {
	context.HTML(http.StatusOK, "index.gohtml", gin.H{})
}

func lnAuthInitHandler(context *gin.Context) {
	k1 := authenticationService.init()

	generateLnUrl(context, k1, "/ln/auth?tag=login&k1="+k1)
}

func lnAuthVerifyHandler(context *gin.Context) {
	k1, sig, key := context.Query(k1Param), context.Query(sigParam), context.Query(keyParam)
	if err := authenticationService.verify(k1, sig, key); err != nil {
		context.Error(fmt.Errorf("authentication failed: %w", err))
		abortWithBadRequestResponse(context, "invalid request")
		return
	}

	context.JSON(http.StatusOK, lnurl.OkResponse())
}

func lnAuthIdentityHandler(context *gin.Context) {
	identity := authenticationService.identity(context.Param("k1"))
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

	amount := context.Query(amountParam)
	if amount == "" {
		var nostrPubkey string
		if account.AllowsNostr {
			nostrPubkey = nostrService.getPublicKey()
		}

		context.JSON(http.StatusOK, LnUrlPayParams{
			Callback:        scheme + "://" + host + context.Request.RequestURI,
			MinSendable:     account.getMinSendable(),
			MaxSendable:     account.getMaxSendable(),
			EncodedMetadata: lnurlMetadata.Encode(),
			CommentAllowed:  int64(account.CommentAllowed),
			AllowsNostr:     account.AllowsNostr,
			NostrPubkey:     nostrPubkey,
			Tag:             payRequestTag,
		})
		return
	}

	msats, err := strconv.ParseInt(amount, 10, 64)
	if err != nil || msats < account.getMinSendable() || msats > account.getMaxSendable() {
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	comment := context.Query(commentParam)
	if len(comment) > int(account.CommentAllowed) {
		abortWithBadRequestResponse(context, "invalid comment length")
		return
	}

	var zapRequest *nostr.Event
	if zapRequestJson := context.Query(nostrParam); zapRequestJson != "" {
		if !account.AllowsNostr {
			abortWithBadRequestResponse(context, "zap requests not allowed")
			return
		}
		zapRequest, err = parseZapRequest(zapRequestJson, amount)
		if err != nil {
			abortWithBadRequestResponse(context, "invalid zap request")
			return
		}
	}

	var descriptionBytes []byte
	if zapRequest != nil {
		descriptionBytes = zapRequest.Serialize()
	} else {
		descriptionBytes = []byte(lnurlMetadata.Encode())
	}

	descriptionHash := sha256.Sum256(descriptionBytes)
	invoice := createInvoice(context, msats, comment, descriptionHash[:])
	if invoice == nil {
		return
	}
	if err := repository.addAccountInvoice(accountKey, invoice); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing invoice: %w", err))
		return
	}

	var successAction *lnurl.SuccessAction
	if strings.TrimSpace(account.SuccessMessage) != "" {
		successAction = successMessage(account.SuccessMessage)
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: successAction,
		Routes:        []string{},
	})

	if zapRequest != nil {
		go awaitSettlement(zapRequest, invoice.getPaymentHash())
	}
}

func lnPayQrCodeHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}

	generateQrCode(context, "/ln/pay/"+accountKey, getAccountThumbnailData(account))
}

func lnRaffleTicketHandler(context *gin.Context) {
	raffle := getRaffle(context)
	if raffle == nil {
		return
	}
	if repository.isRaffleDrawAvailable(raffle) {
		abortWithBadRequestResponse(context, "raffle already drawn")
		return
	}

	ticketPrice := msats(raffle.TicketPrice)

	var lnurlMetadata lnurl.Metadata
	lnurlMetadata.Description = raffle.Title
	lnurlMetadata.Image.Bytes = tombolaPngData
	lnurlMetadata.Image.Ext = "png"

	if thumbnail := getRaffleThumbnail(raffle); thumbnail != nil {
		lnurlMetadata.Image.Bytes = thumbnail.bytes
		lnurlMetadata.Image.Ext = thumbnail.ext
	}

	amount := context.Query(amountParam)
	if amount == "" {
		scheme, host := getSchemeAndHost(context)
		context.JSON(http.StatusOK, lnurl.LNURLPayParams{
			Callback:        scheme + "://" + host + context.Request.RequestURI,
			MinSendable:     ticketPrice,
			MaxSendable:     ticketPrice,
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

	if len(context.Query(commentParam)) > 0 {
		abortWithBadRequestResponse(context, "comment not supported")
		return
	}

	descriptionHash := sha256.Sum256([]byte(lnurlMetadata.Encode()))
	invoice := createInvoice(context, msats, "", descriptionHash[:])
	if invoice == nil {
		return
	}
	if err := repository.addRaffleTicket(raffle, invoice); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing ticket: %w", err))
		return
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: successMessage("Ticket " + raffleTicketNumber(invoice.getPaymentHash())),
		Routes:        []string{},
	})
}

func lnRaffleQrCodeHandler(context *gin.Context) {
	raffle := getRaffle(context)
	if raffle == nil {
		return
	}
	if repository.isRaffleDrawAvailable(raffle) {
		abortWithBadRequestResponse(context, "raffle already drawn")
		return
	}

	thumbnailData := tombolaPngData
	if thumbnail := getRaffleThumbnail(raffle); thumbnail != nil {
		thumbnailData = thumbnail.bytes
	}

	generateQrCode(context, "/ln/raffle/"+raffle.Id, thumbnailData)
}

func lnWithdrawConfirmHandler(context *gin.Context) {
	k1 := context.Query(k1Param)
	withdrawalRequest := withdrawalService.get(k1)
	if withdrawalRequest == nil {
		abortWithNotFoundResponse(context)
		return
	}

	pr := context.Query(prParam)
	paymentHash, amount := lndClient.decodePaymentRequest(pr)
	if paymentHash == "" || amount != withdrawalRequest.amount {
		abortWithBadRequestResponse(context, "invalid payment request")
		return
	}

	if err := repository.createWithdrawal(withdrawalRequest, paymentHash); err != nil {
		abortWithNotFoundResponse(context)
		return
	}
	if err := lndClient.sendPayment(pr); err != nil {
		abortWithInternalServerErrorResponse(context, err)
		return
	}
	withdrawalService.remove(k1)

	context.JSON(http.StatusOK, lnurl.OkResponse())
}

func lnWithdrawRequestHandler(context *gin.Context) {
	k1 := context.Param("k1")
	withdrawalRequest := withdrawalService.get(k1)
	if withdrawalRequest == nil {
		abortWithNotFoundResponse(context)
		return
	}

	scheme, host := getSchemeAndHost(context)
	withdrawable := msats(withdrawalRequest.amount)

	context.JSON(http.StatusOK, lnurl.LNURLWithdrawResponse{
		Tag:                withdrawRequestTag,
		K1:                 k1,
		Callback:           scheme + "://" + host + "/ln/withdraw",
		MinWithdrawable:    withdrawable,
		MaxWithdrawable:    withdrawable,
		DefaultDescription: withdrawalRequest.description,
	})
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
		"Start":           event.Start,
		"Location":        event.Location,
		"Capacity":        event.Capacity,
		"Description":     event.getDescriptionParagraphs(),
		"Attendees":       len(attendees),
		"AttendeeOrdinal": slices.Index(attendees, identity) + 1,
		"SignUpPossible":  event.Start.After(time.Now()),
		"LnAuthExpiry":    config.Authentication.RequestExpiry.Milliseconds(),
		"IdentityId":      toIdentityId(identity),
	})
}

func eventIcsHandler(context *gin.Context) {
	event := getEvent(context)
	if event == nil {
		return
	}

	_, host := getSchemeAndHost(context)
	icsFileName := "event-" + event.Id + ".ics"
	icsData := iCalendarEvent(event, host)

	context.Header("Content-Disposition", `attachment; filename="`+icsFileName+`"`)
	context.Data(http.StatusOK, "text/calendar", []byte(icsData))
}

func eventSignUpHandler(context *gin.Context) {
	event := getEvent(context)
	if event == nil {
		return
	}
	if event.Start.Before(time.Now()) {
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
	context.HTML(http.StatusOK, "auth.gohtml", gin.H{
		"AccountsCount": len(getAccessibleAccounts(context)),
	})
}

func authAccountsHandler(context *gin.Context) {
	authenticatedUser := getAuthenticatedUser(context)
	userState := repository.getUserState(authenticatedUser)

	accounts := map[string]AccountSummary{}
	for accountKey, account := range getAccessibleAccounts(context) {
		currentInvoicesCount := repository.getAccountInvoicesCount(accountKey)
		previousInvoicesCount := userState.AccountInvoicesCounts[accountKey]
		accounts[accountKey] = AccountSummary{
			Description:      account.Description,
			NewInvoicesCount: currentInvoicesCount - previousInvoicesCount,
		}
	}

	context.HTML(http.StatusOK, "accounts.gohtml", gin.H{
		"Accounts": accounts,
	})
}

func authAccountHandler(context *gin.Context) {
	accountKey, account := getAccessibleAccount(context)
	if accountKey == "" {
		return
	}

	authenticatedUser := getAuthenticatedUser(context)
	userState := repository.getUserState(authenticatedUser)
	previousInvoicesCount := userState.AccountInvoicesCounts[accountKey]

	invoices := repository.getAccountInvoices(accountKey)
	invoicesIssued := len(invoices)

	var invoicesSettled int
	var totalSatsReceived int64
	var commentsCount int
	var accountInvoices []AccountInvoice
	for i, paymentHash := range invoices {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.isSettled() {
			invoicesSettled++
			totalSatsReceived += invoice.amount
			if invoice.memo != "" {
				commentsCount++
			}
			accountInvoices = append(accountInvoices, AccountInvoice{
				Amount:     invoice.amount,
				SettleDate: invoice.settleDate,
				Comment:    invoice.memo,
				IsNew:      i >= previousInvoicesCount,
			})
		}
	}

	userState.AccountInvoicesCounts[accountKey] = invoicesIssued
	if err := repository.updateUserState(authenticatedUser, userState); err != nil {
		log.Println("error updating user state:", err)
	}

	sort.Slice(accountInvoices, func(i, j int) bool {
		return accountInvoices[i].SettleDate.After(accountInvoices[j].SettleDate)
	})

	context.HTML(http.StatusOK, "account.gohtml", gin.H{
		"AccountKey":        accountKey,
		"FiatCurrency":      account.getCurrency(),
		"InvoicesIssued":    invoicesIssued,
		"InvoicesSettled":   invoicesSettled,
		"CommentsCount":     commentsCount,
		"TotalSatsReceived": totalSatsReceived,
		"TotalFiatReceived": ratesService.satsToFiat(account.getCurrency(), totalSatsReceived),
		"Archivable":        account.Archivable && invoicesSettled > 0,
		"Invoices":          accountInvoices,
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
	authenticatedUser := getAuthenticatedUser(context)

	var events []*Event
	var pastEvents []*Event
	for _, event := range repository.getEvents() {
		if isUserAuthorized(context, event.Owner) {
			event.IsMine = event.Owner == authenticatedUser
			if event.isInPast() {
				pastEvents = append(pastEvents, event)
			} else {
				events = append(events, event)
			}
		}
	}

	context.HTML(http.StatusOK, "events.gohtml", gin.H{
		"Events":     sortEvents(events),
		"PastEvents": sortPastEvents(pastEvents),
	})
}

func authRafflesHandler(context *gin.Context) {
	authenticatedUser := getAuthenticatedUser(context)

	var raffles []*Raffle
	var drawnRaffles []*Raffle
	for _, raffle := range repository.getRaffles() {
		if isUserAuthorized(context, raffle.Owner) {
			raffle.IsMine = raffle.Owner == authenticatedUser
			if repository.isRaffleDrawAvailable(raffle) {
				drawnRaffles = append(drawnRaffles, raffle)
			} else {
				raffles = append(raffles, raffle)
			}
		}
	}

	context.HTML(http.StatusOK, "raffles.gohtml", gin.H{
		"Raffles":        sortRaffles(raffles),
		"DrawnRaffles":   sortRaffles(drawnRaffles),
		"FiatCurrencies": supportedCurrencies(),
		"ExchangeRates":  ratesService.getExchangeRates(),
	})
}

func authRaffleHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	tickets := repository.getRaffleTickets(raffle)
	drawAvailable := repository.isRaffleDrawAvailable(raffle)
	withdrawalFinished := repository.isRaffleWithdrawalFinished(raffle)
	locked := repository.isRaffleLocked(raffle)

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
		"Id":                 raffle.Id,
		"Title":              raffle.Title,
		"TicketPrice":        raffle.TicketPrice,
		"FiatCurrency":       raffle.FiatCurrency,
		"PrizesCount":        raffle.GetPrizesCount(),
		"TicketsIssued":      len(tickets),
		"TicketsPaid":        ticketsPaid,
		"TotalSatsReceived":  totalSatsReceived,
		"TotalFiatReceived":  ratesService.satsToFiat(raffle.FiatCurrency, totalSatsReceived),
		"DrawAvailable":      drawAvailable,
		"Withdrawable":       drawAvailable && !withdrawalFinished && !locked,
		"WithdrawalFinished": withdrawalFinished,
		"WithdrawalExpiry":   config.Withdrawal.RequestExpiry.Milliseconds(),
		"Lockable":           drawAvailable && !withdrawalFinished && !locked && isAdministrator(context),
	})
}

func authRaffleDrawHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	raffleDraw := getRaffleDraw(context, raffle)
	if raffleDraw == nil {
		return
	}

	var drawnTickets []RaffleTicket
	for _, paymentHash := range raffleDraw {
		drawnTickets = append(drawnTickets, RaffleTicket{
			Number:      raffleTicketNumber(paymentHash),
			PaymentHash: paymentHash[0:5] + "…" + paymentHash[59:],
		})
	}

	context.HTML(http.StatusOK, "draw.gohtml", gin.H{
		"Title":        raffle.Title,
		"Prizes":       raffle.GetPrizes(),
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

	err := repository.archiveAccountInvoices(accountKey)
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
	if err := repository.addAccountInvoice(accountKey, invoice); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing invoice: %w", err))
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
	event.Owner = getAuthenticatedUser(context)

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
	if event.isInPast() {
		abortWithBadRequestResponse(context, "not updatable")
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
	raffle.Owner = getAuthenticatedUser(context)

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
	if repository.isRaffleDrawAvailable(raffle) {
		abortWithBadRequestResponse(context, "not updatable")
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

func apiRaffleWithdrawHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}
	if repository.isRaffleWithdrawalFinished(raffle) || repository.isRaffleLocked(raffle) {
		abortWithBadRequestResponse(context, "not withdrawable")
		return
	}

	var totalSatsReceived int64
	for _, paymentHash := range repository.getRaffleDraw(raffle) {
		if invoice, _ := lndClient.getInvoice(paymentHash); invoice != nil {
			totalSatsReceived += invoice.amount
		}
	}

	k1 := withdrawalService.init(
		repository.getRaffleWithdrawalFileName(raffle),
		totalSatsReceived,
		raffle.Title,
	)

	generateLnUrl(context, k1, "/ln/withdraw/"+k1)
}

func apiRaffleLockHandler(context *gin.Context) {
	if !isAdministrator(context) {
		abortWithNotFoundResponse(context)
		return
	}

	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}
	if !repository.isRaffleDrawAvailable(raffle) || repository.isRaffleWithdrawalFinished(raffle) {
		abortWithBadRequestResponse(context, "not lockable")
		return
	}

	err := repository.lockRaffle(raffle)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("locking raffle: %w", err))
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

func getAccessibleAccounts(context *gin.Context) map[string]Account {
	accessibleAccounts := map[string]Account{}
	for accountKey, account := range config.Accounts {
		if isAccountAccessible(context, accountKey) {
			accessibleAccounts[accountKey] = account
		}
	}

	return accessibleAccounts
}

func isAccountAccessible(context *gin.Context, accountKey string) bool {
	authenticatedUser := getAuthenticatedUser(context)
	if slices.Contains(config.Administrators, authenticatedUser) {
		return true
	}
	return slices.Contains(config.AccessControl[authenticatedUser], accountKey)
}

func getAccountThumbnail(account *Account) *Thumbnail {
	if account.Thumbnail == "" {
		return nil
	}

	thumbnail, err := repository.getThumbnail(account.Thumbnail)
	if err != nil {
		log.Println("error reading thumbnail:", err)
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

func getRaffleThumbnail(raffle *Raffle) *Thumbnail {
	userThumbnail := config.Thumbnails[raffle.Owner]
	if userThumbnail == "" {
		return nil
	}

	thumbnail, err := repository.getThumbnail(userThumbnail)
	if err != nil {
		log.Println("error reading thumbnail:", err)
	}

	return thumbnail
}

func getRaffleDraw(context *gin.Context, raffle *Raffle) []string {
	raffleDraw := repository.getRaffleDraw(raffle)
	if len(raffleDraw) > 0 {
		return raffleDraw
	}

	for _, paymentHash := range repository.getRaffleTickets(raffle) {
		invoice, _ := lndClient.getInvoice(paymentHash)
		if invoice != nil && invoice.isSettled() {
			raffleDraw = append(raffleDraw, paymentHash)
		}
	}

	if len(raffleDraw) < raffle.GetPrizesCount() {
		abortWithBadRequestResponse(context, "not enough tickets")
		return nil
	}

	rand.Shuffle(len(raffleDraw), func(i, j int) {
		raffleDraw[i], raffleDraw[j] = raffleDraw[j], raffleDraw[i]
	})

	if err := repository.createRaffleDraw(raffle, raffleDraw); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing raffle draw: %w", err))
		return nil
	}

	return raffleDraw
}

func isUserAuthorized(context *gin.Context, owner string) bool {
	authenticatedUser := getAuthenticatedUser(context)
	return authenticatedUser == owner || slices.Contains(config.Administrators, authenticatedUser)
}

func isAdministrator(context *gin.Context) bool {
	authenticatedUser := getAuthenticatedUser(context)
	return slices.Contains(config.Administrators, authenticatedUser)
}

func getAuthenticatedUser(context *gin.Context) string {
	return context.GetString(gin.AuthUserKey)
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

func awaitSettlement(zapRequest *nostr.Event, paymentHash string) {
	for i := 0; i < invoiceExpiryInSeconds; i++ {
		invoice, _ := lndClient.getInvoice(paymentHash)
		if invoice != nil && invoice.isSettled() {
			go nostrService.publishZapReceipt(zapRequest, invoice)
			return
		}
		time.Sleep(1 * time.Second)
	}
}

func generateLnUrl(context *gin.Context, k1 string, uri string) {
	scheme, host := getSchemeAndHost(context)
	lnUrl, err := lnurl.LNURLEncode(scheme + "://" + host + uri)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding LNURL: %w", err))
		return
	}

	pngData, err := encodeQrCode(lnUrl, lightningPngData, qrCodeSize, true)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.JSON(http.StatusOK, LnUrlData{
		K1:     k1,
		LnUrl:  lnUrl,
		QrCode: pngDataUrl(pngData),
	})
}

func generateQrCode(context *gin.Context, uri string, thumbnailData []byte) {
	scheme, host := getSchemeAndHost(context)
	lnUrl, err := lnurl.LNURLEncode(scheme + "://" + host + uri)
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

	pngData, err := encodeQrCode(lnUrl, thumbnailData, int(size), false)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.Data(http.StatusOK, "image/png", pngData)
}

func setCacheControlHeader(context *gin.Context) {
	context.Header("Cache-Control", "no-store, must-revalidate")
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
