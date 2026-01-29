package main

import (
	"crypto/sha256"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fiatjaf/go-lnurl"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/nbd-wtf/go-nostr"
)

type LnUrlData struct {
	K1     string `json:"k1"`
	LnUrl  string `json:"lnUrl"`
	QrCode string `json:"qrCode"`
}

type LnAuthIdentity struct {
	Identity Identity `json:"identity"`
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
	AccountKey AccountKey `json:"accountKey"`
	Amount     string     `json:"amount"`
}

type InvoiceResponse struct {
	PaymentHash PaymentHash `json:"paymentHash"`
	QrCode      string      `json:"qrCode"`
}

type InvoiceStatus struct {
	Settled bool `json:"settled"`
}

const (
	sessionIdentityKey = "identity"
	sessionTokenKey    = "token"
	qrCodeSize         = 1280
)

var (
	//go:embed files/static
	staticFs embed.FS
	//go:embed files/lightning.png
	lightningPngData []byte
	//go:embed files/raffle.png
	rafflePngData []byte

	config                *Config
	repository            *Repository
	lndClient             *LndClient
	authenticationService *AuthenticationService
	withdrawalService     *WithdrawalService
	raffleService         *RaffleService
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
	authenticationService = newAuthenticationService(config.Credentials, config.Authentication)
	withdrawalService = newWithdrawalService(config.Withdrawal)
	raffleService = newRaffleService(repository, lndClient)
	nostrService = newNostrService(config.DataDir, config.Nostr)
	ratesService = newRatesService(30 * time.Second)

	lnurld := gin.Default()
	_ = lnurld.SetTrustedProxies(nil)
	loadTemplates(lnurld, "files/templates/*.gohtml")

	lnurld.NoRoute(abortWithNotFoundResponse)

	public := lnurld.Group("/", sessionHandler("lnSession", 42))
	public.GET("/", indexHandler)
	public.POST("/ln/auth", lnAuthInitHandler)
	public.GET("/ln/auth", lnAuthVerifyHandler)
	public.GET("/ln/auth/:k1", lnAuthIdentityHandler)
	public.GET("/.well-known/lnurlp/:name", lnPayHandler)
	public.GET("/ln/pay/:name", lnPayHandler)
	public.GET("/ln/pay/:name/qr-code", lnPayQrCodeHandler)
	public.GET("/ln/raffle/:id", lnRaffleTicketHandler)
	public.GET("/ln/raffle/:id/qr-code", lnRaffleQrCodeHandler)
	public.GET("/ln/withdraw", lnWithdrawConfirmHandler)
	public.GET("/ln/withdraw/:k1", lnWithdrawRequestHandler)
	public.GET("/events/:id", eventHandler)
	public.GET("/events/:id/ics", eventIcsHandler)
	public.POST("/events/:id/sign-up", eventSignUpHandler)
	public.GET("/raffles/:id", raffleHandler)
	public.GET("/static/*filepath", lnStaticFileHandler)

	authentication := lnurld.Group("/", sessionHandler("session", 7), noCacheHandler)
	authentication.GET("/login", loginFormHandler)
	authentication.POST("/login", loginSubmitHandler)

	authorized := authentication.Group("/", authAuthorizationHandler)
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
	authorized.POST("/api/raffles/:id/draw", apiRaffleDrawCommitHandler)
	authorized.POST("/api/raffles/:id/withdraw", apiRaffleWithdrawHandler)
	authorized.POST("/api/raffles/:id/lock", apiRaffleLockHandler)

	log.Fatal(lnurld.Run(config.Listen))
}

func sessionHandler(name string, maxAgeDays int) gin.HandlerFunc {
	sessionStore := cookie.NewStore(config.cookieKey())
	sessionStore.Options(sessions.Options{
		Path:     "/",
		MaxAge:   maxAgeDays * 86400,
		Secure:   gin.Mode() == gin.ReleaseMode,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	return sessions.Sessions(name, sessionStore)
}

func noCacheHandler(context *gin.Context) {
	context.Header("Cache-Control", "no-store, must-revalidate")
}

func indexHandler(context *gin.Context) {
	context.HTML(http.StatusOK, "index.gohtml", gin.H{})
}

func lnAuthInitHandler(context *gin.Context) {
	k1 := authenticationService.generateChallenge()

	generateLnUrl(context, k1, "/ln/auth?tag=login&k1="+k1)
}

func lnAuthVerifyHandler(context *gin.Context) {
	k1, sig, key := context.Query(k1Param), context.Query(sigParam), context.Query(keyParam)
	if err := authenticationService.verifyChallenge(k1, sig, key); err != nil {
		context.Error(fmt.Errorf("authentication failed: %w", err))
		abortWithBadRequestResponse(context, "invalid request")
		return
	}

	context.JSON(http.StatusOK, lnurl.OkResponse())
}

func lnAuthIdentityHandler(context *gin.Context) {
	identity := authenticationService.getIdentity(context.Param("k1"))
	if identity == "" {
		abortWithNotFoundResponse(context)
		return
	}

	session := sessions.Default(context)
	session.Set(sessionIdentityKey, string(identity))
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
		LightningAddress: string(accountKey) + "@" + host,
		IsEmail:          account.IsAlsoEmail,
	}

	if thumbnail := getAccountThumbnail(account); thumbnail != nil {
		lnurlMetadata.Image.Bytes = thumbnail.bytes
		lnurlMetadata.Image.Ext = thumbnail.ext
	}

	amountString := context.Query(amountParam)
	if amountString == "" {
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

	amount, err := parseAmount(amountString)
	if err != nil || amount < account.getMinSendable() || amount > account.getMaxSendable() {
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
		zapRequest, err = parseZapRequest(zapRequestJson, amountString)
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
	invoice := createInvoice(context, amount, comment, descriptionHash[:])
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
		go awaitSettlement(zapRequest, invoice.paymentHash)
	}
}

func lnPayQrCodeHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}

	generateQrCode(context, "/ln/pay/"+string(accountKey), getAccountThumbnailData(account))
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

	quantity := getRequestedQuantity(context)
	if quantity < 0 {
		return
	}

	var lnurlMetadata lnurl.Metadata
	lnurlMetadata.Description = raffle.description(quantity)
	lnurlMetadata.Image.Bytes = rafflePngData
	lnurlMetadata.Image.Ext = "png"

	if thumbnail := getRaffleThumbnail(raffle); thumbnail != nil {
		lnurlMetadata.Image.Bytes = thumbnail.bytes
		lnurlMetadata.Image.Ext = thumbnail.ext
	}

	amountString := context.Query(amountParam)
	if amountString == "" {
		scheme, host := getSchemeAndHost(context)
		sendable := raffle.sendable(quantity)
		context.JSON(http.StatusOK, lnurl.LNURLPayParams{
			Callback:        scheme + "://" + host + context.Request.RequestURI,
			MinSendable:     sendable,
			MaxSendable:     sendable,
			EncodedMetadata: lnurlMetadata.Encode(),
			Tag:             payRequestTag,
		})
		return
	}

	amount, err := parseAmount(amountString)
	if err != nil || amount != raffle.sendable(quantity) {
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	if len(context.Query(commentParam)) > 0 {
		abortWithBadRequestResponse(context, "comment not supported")
		return
	}

	descriptionHash := sha256.Sum256([]byte(lnurlMetadata.Encode()))
	invoice := createInvoice(context, amount, "", descriptionHash[:])
	if invoice == nil {
		return
	}

	tickets := RaffleTickets{invoice.paymentHash, quantity}
	if err := repository.addRaffleTickets(raffle, tickets); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing ticket: %w", err))
		return
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: successMessage(raffle.successMessage(tickets)),
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

	quantity := getRequestedQuantity(context)
	if quantity < 0 {
		return
	}

	thumbnailData := rafflePngData
	if thumbnail := getRaffleThumbnail(raffle); thumbnail != nil {
		thumbnailData = thumbnail.bytes
	}

	generateQrCode(context, lnRaffleTicketUri(raffle, quantity), thumbnailData)
}

func lnWithdrawConfirmHandler(context *gin.Context) {
	k1 := context.Query(k1Param)
	withdrawalRequest := withdrawalService.getRequest(k1)
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
	if err := lndClient.sendPayment(pr, withdrawalRequest.feeLimit); err != nil {
		abortWithInternalServerErrorResponse(context, err)
		return
	}
	withdrawalService.removeRequest(k1)

	context.JSON(http.StatusOK, lnurl.OkResponse())
}

func lnWithdrawRequestHandler(context *gin.Context) {
	k1 := context.Param("k1")
	withdrawalRequest := withdrawalService.getRequest(k1)
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
		"Description":     event.descriptionParagraphs(),
		"Attendees":       len(attendees),
		"AttendeeOrdinal": slices.Index(attendees, identity) + 1,
		"SignUpPossible":  event.Start.After(time.Now()),
		"LnAuthExpiry":    config.Authentication.RequestExpiry.Milliseconds(),
		"Identity":        identity,
	})
}

func eventIcsHandler(context *gin.Context) {
	event := getEvent(context)
	if event == nil {
		return
	}

	_, host := getSchemeAndHost(context)
	icsFileName := "event-" + string(event.Id) + ".ics"
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

func raffleHandler(context *gin.Context) {
	raffle := getRaffle(context)
	if raffle == nil {
		return
	}

	scheme, host := getSchemeAndHost(context)

	var qrCodes []RaffleQrCode
	if !repository.isRaffleDrawAvailable(raffle) {
		for quantity := minQuantity; quantity <= maxQuantity; quantity++ {
			lnRaffleTicketUrl := scheme + "://" + host + lnRaffleTicketUri(raffle, quantity)
			if lnUrl, err := lnurl.LNURLEncode(lnRaffleTicketUrl); err == nil {
				qrCodes = append(qrCodes, RaffleQrCode{
					LnUrl: lnUrl,
					Uri:   lnRaffleQrCodeUri(raffle, quantity),
				})
			}
		}
	}

	context.HTML(http.StatusOK, "raffle-public.gohtml", gin.H{
		"Title":       raffle.Title,
		"PrizesCount": raffle.PrizesCount(),
		"QrCodes":     qrCodes,
	})
}

func lnStaticFileHandler(context *gin.Context) {
	filePath := path.Join("files/static", context.Param("filepath"))
	context.FileFromFS(filePath, http.FS(staticFs))
}

func loginFormHandler(context *gin.Context) {
	if getAuthenticatedUser(context) != "" {
		context.Redirect(http.StatusFound, "/auth")
		return
	}

	context.HTML(http.StatusOK, "login.gohtml", gin.H{})
}

func loginSubmitHandler(context *gin.Context) {
	username := UserKey(context.PostForm("username"))
	password := context.PostForm("password")

	if !authenticationService.verifyCredentials(username, password) {
		context.HTML(http.StatusOK, "login.gohtml", gin.H{
			"InvalidCredentials": true,
		})
		return
	}

	if err := setAuthenticatedUser(context, username); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("creating session: %w", err))
		return
	}

	context.Redirect(http.StatusFound, "/auth")
}

func authAuthorizationHandler(context *gin.Context) {
	authenticatedUser := getAuthenticatedUser(context)
	if authenticatedUser == "" {
		context.Redirect(http.StatusFound, "/login")
		context.Abort()
		return
	}

	if err := setAuthenticatedUser(context, authenticatedUser); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("renewing session: %w", err))
		return
	}
}

func authHomeHandler(context *gin.Context) {
	context.HTML(http.StatusOK, "auth.gohtml", gin.H{
		"AccountsCount": len(getAccessibleAccounts(context)),
	})
}

func authAccountsHandler(context *gin.Context) {
	authenticatedUser := getAuthenticatedUser(context)
	userState := repository.getUserState(authenticatedUser)

	accounts := map[AccountKey]AccountSummary{}
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
		invoice := lndClient.getInvoice(paymentHash)
		if invoice != nil && invoice.isSettled() {
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

	drawAvailable := repository.isRaffleDrawAvailable(raffle)
	drawFinished := repository.isRaffleDrawFinished(raffle)
	withdrawalFinished := repository.isRaffleWithdrawalFinished(raffle)
	locked := repository.isRaffleLocked(raffle)

	var ticketsIssued int
	var ticketsPaid int
	var totalSatsReceived int64
	for _, tickets := range repository.getRaffleTickets(raffle) {
		ticketsIssued += tickets.quantity
		invoice := lndClient.getInvoice(tickets.paymentHash)
		if invoice != nil && invoice.isSettled() {
			ticketsPaid += tickets.quantity
			totalSatsReceived += invoice.amount
		}
	}

	context.HTML(http.StatusOK, "raffle.gohtml", gin.H{
		"Id":                 raffle.Id,
		"Title":              raffle.Title,
		"TicketPrice":        raffle.TicketPrice,
		"FiatCurrency":       raffle.FiatCurrency,
		"PrizesCount":        raffle.PrizesCount(),
		"TicketsIssued":      ticketsIssued,
		"TicketsPaid":        ticketsPaid,
		"TotalSatsReceived":  totalSatsReceived,
		"TotalFiatReceived":  ratesService.satsToFiat(raffle.FiatCurrency, totalSatsReceived),
		"DrawAvailable":      drawAvailable,
		"DrawFinished":       drawFinished,
		"Withdrawable":       drawFinished && !withdrawalFinished && !locked,
		"WithdrawalFinished": withdrawalFinished,
		"WithdrawalExpiry":   config.Withdrawal.RequestExpiry.Milliseconds(),
		"Lockable":           drawFinished && !withdrawalFinished && !locked && isAdministrator(context),
	})
}

func authRaffleDrawHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}

	if prizeWinners := raffleService.getPrizeWinners(raffle); prizeWinners != nil {
		context.HTML(http.StatusOK, "winners.gohtml", gin.H{
			"Title":        raffle.Title,
			"PrizeWinners": prizeWinners,
		})
		return
	}

	raffleDraw := getRaffleDraw(context, raffle)
	if raffleDraw == nil {
		return
	}

	context.HTML(http.StatusOK, "draw.gohtml", gin.H{
		"Id":           raffle.Id,
		"Title":        raffle.Title,
		"Prizes":       raffle.prizes(),
		"DrawnTickets": raffleService.getDrawnTickets(raffleDraw),
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

	usersWithAccountAccess := config.Administrators
	for user, allowedAccounts := range config.AccessControl {
		if slices.Contains(allowedAccounts, accountKey) {
			usersWithAccountAccess = append(usersWithAccountAccess, user)
		}
	}

	for _, user := range usersWithAccountAccess {
		userState := repository.getUserState(user)
		if userState.AccountInvoicesCounts[accountKey] > 0 {
			userState.AccountInvoicesCounts[accountKey] = 0
			if err := repository.updateUserState(user, userState); err != nil {
				log.Println("error updating user state:", err)
			}
		}
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

	amountString, err := strconv.ParseFloat(request.Amount, 32)
	if err != nil || amountString <= 0 || amountString >= 1_000_000 {
		abortWithBadRequestResponse(context, "invalid amount")
		return
	}

	amount := msats(ratesService.fiatToSats(account.getCurrency(), amountString))
	invoice := createInvoice(context, amount, "", []byte{})
	if invoice == nil {
		return
	}
	if err := repository.addAccountInvoice(accountKey, invoice); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing invoice: %w", err))
		return
	}

	thumbnailData := getAccountThumbnailData(&account)
	pngData, err := encodeQrCode(strings.ToUpper(invoice.paymentRequest), thumbnailData, qrCodeSize)
	if err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("encoding QR code: %w", err))
		return
	}

	context.JSON(http.StatusOK, InvoiceResponse{
		PaymentHash: invoice.paymentHash,
		QrCode:      pngDataUrl(pngData),
	})
}

func apiInvoiceStatusHandler(context *gin.Context) {
	paymentHash := PaymentHash(context.Param("paymentHash"))
	invoice := lndClient.getInvoice(paymentHash)
	if invoice == nil {
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

func apiRaffleDrawCommitHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}
	if !repository.isRaffleDrawAvailable(raffle) || repository.isRaffleDrawFinished(raffle) {
		abortWithBadRequestResponse(context, "not commitable")
		return
	}

	var raffleDrawCommit RaffleDrawCommit
	if err := context.BindJSON(&raffleDrawCommit); err != nil {
		abortWithBadRequestResponse(context, err.Error())
		return
	}

	raffleDraw := repository.getRaffleDraw(raffle)
	skippedTickets := raffleDrawCommit.SkippedTickets
	slices.DeleteFunc(raffleDraw, func(ticket RaffleTicket) bool {
		if slices.Contains(skippedTickets, ticket.String()) {
			skippedTickets = skippedTickets[1:]
			return true
		}
		return false
	})

	prizesCount := raffle.PrizesCount()
	if len(raffleDraw) < prizesCount || len(skippedTickets) > 0 {
		abortWithBadRequestResponse(context, "invalid commit request")
		return
	}

	raffleWinners := raffleDraw[0:prizesCount]
	slices.Reverse(raffleWinners)

	if err := repository.createRaffleWinners(raffle, raffleWinners); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing raffle winners: %w", err))
		return
	}

	context.Status(http.StatusNoContent)
}

func apiRaffleWithdrawHandler(context *gin.Context) {
	raffle := getAccessibleRaffle(context)
	if raffle == nil {
		return
	}
	if !repository.isRaffleDrawFinished(raffle) || repository.isRaffleWithdrawalFinished(raffle) || repository.isRaffleLocked(raffle) {
		abortWithBadRequestResponse(context, "not withdrawable")
		return
	}

	var totalSatsReceived int64
	for _, tickets := range repository.getRaffleTickets(raffle) {
		invoice := lndClient.getInvoice(tickets.paymentHash)
		if invoice != nil && invoice.isSettled() {
			totalSatsReceived += invoice.amount
		}
	}

	k1 := withdrawalService.createRequest(
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
	if !repository.isRaffleDrawFinished(raffle) || repository.isRaffleWithdrawalFinished(raffle) {
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

func lnRaffleTicketUri(raffle *Raffle, quantity int) string {
	return "/ln/raffle/" + string(raffle.Id) + "?" + quantityParam + "=" + strconv.Itoa(quantity)
}

func lnRaffleQrCodeUri(raffle *Raffle, quantity int) string {
	id, q, s := string(raffle.Id), strconv.Itoa(quantity), strconv.Itoa(qrCodeSize)
	return "/ln/raffle/" + id + "/qr-code?" + quantityParam + "=" + q + "&" + sizeParam + "=" + s
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

func getIdentity(context *gin.Context) Identity {
	session := sessions.Default(context)
	if identity := session.Get(sessionIdentityKey); identity != nil {
		return Identity(identity.(string))
	}
	return ""
}

func getAccount(context *gin.Context) (AccountKey, *Account) {
	accountKey := AccountKey(context.Param("name"))
	if account, accountExists := config.Accounts[accountKey]; accountExists {
		return accountKey, &account
	}

	abortWithNotFoundResponse(context)
	return "", nil
}

func getAccessibleAccount(context *gin.Context) (AccountKey, *Account) {
	accountKey, account := getAccount(context)
	if account == nil || isAccountAccessible(context, accountKey) {
		return accountKey, account
	}

	abortWithNotFoundResponse(context)
	return "", nil
}

func getAccessibleAccounts(context *gin.Context) map[AccountKey]Account {
	accessibleAccounts := map[AccountKey]Account{}
	for accountKey, account := range config.Accounts {
		if isAccountAccessible(context, accountKey) {
			accessibleAccounts[accountKey] = account
		}
	}

	return accessibleAccounts
}

func isAccountAccessible(context *gin.Context, accountKey AccountKey) bool {
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
	eventId := EventId(context.Param("id"))
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
	raffleId := RaffleId(context.Param("id"))
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

func getRaffleDraw(context *gin.Context, raffle *Raffle) []RaffleTicket {
	raffleDraw := repository.getRaffleDraw(raffle)
	if len(raffleDraw) > 0 {
		return raffleDraw
	}

	for _, tickets := range repository.getRaffleTickets(raffle) {
		invoice := lndClient.getInvoice(tickets.paymentHash)
		if invoice != nil && invoice.isSettled() {
			for i := 0; i < tickets.quantity; i++ {
				raffleDraw = append(raffleDraw, RaffleTicket{tickets.paymentHash, i})
			}
		}
	}

	if len(raffleDraw) < raffle.PrizesCount() {
		abortWithBadRequestResponse(context, "not enough tickets")
		return nil
	}

	shuffleRaffleTickets(raffleDraw) // separates tickets related to one invoice
	shuffleRaffleTickets(raffleDraw) // prepares the final draw

	if err := repository.createRaffleDraw(raffle, raffleDraw); err != nil {
		abortWithInternalServerErrorResponse(context, fmt.Errorf("storing raffle draw: %w", err))
		return nil
	}

	return raffleDraw
}

func getRequestedQuantity(context *gin.Context) int {
	quantityString := context.DefaultQuery(quantityParam, "1")
	quantity, err := strconv.ParseInt(quantityString, 10, 32)
	if err == nil && quantity >= minQuantity && quantity <= maxQuantity {
		return int(quantity)
	}

	abortWithBadRequestResponse(context, "invalid quantity")
	return -1
}

func isUserAuthorized(context *gin.Context, owner UserKey) bool {
	authenticatedUser := getAuthenticatedUser(context)
	return authenticatedUser == owner || slices.Contains(config.Administrators, authenticatedUser)
}

func isAdministrator(context *gin.Context) bool {
	authenticatedUser := getAuthenticatedUser(context)
	return slices.Contains(config.Administrators, authenticatedUser)
}

func getAuthenticatedUser(context *gin.Context) UserKey {
	session := sessions.Default(context)
	if token := session.Get(sessionTokenKey); token != nil {
		return authenticationService.getUser(token.(string))
	}
	return ""
}

func setAuthenticatedUser(context *gin.Context, user UserKey) error {
	session := sessions.Default(context)
	session.Set(sessionTokenKey, authenticationService.getToken(user))
	return session.Save()
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

func awaitSettlement(zapRequest *nostr.Event, paymentHash PaymentHash) {
	for i := 0; i < invoiceExpiryInSeconds; i++ {
		invoice := lndClient.getInvoice(paymentHash)
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

	pngData, err := encodeQrCode(lnUrl, lightningPngData, qrCodeSize)
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

	requestedSize := context.DefaultQuery(sizeParam, "256")
	size, err := strconv.ParseUint(requestedSize, 10, 12)
	if err != nil {
		abortWithBadRequestResponse(context, "invalid size")
		return
	}

	pngData, err := encodeQrCode(lnUrl, thumbnailData, int(size))
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

func parseAmount(amount string) (int64, error) {
	return strconv.ParseInt(amount, 10, 64)
}
