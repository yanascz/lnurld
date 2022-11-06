package main

import (
	"crypto/sha256"
	"embed"
	"flag"
	"github.com/fiatjaf/go-lnurl"
	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
)

type LnAccount struct {
	AccountKey        string
	InvoicesIssued    int
	InvoicesSettled   int
	TotalSatsReceived int64
	Raffle            *LnAccountRaffle
}

type LnAccountRaffle struct {
	TicketPrice uint32
	PrizesCount int
}

type LnRaffle struct {
	Prizes       []string
	DrawnTickets []string
}

var (
	//go:embed files/static
	staticFs embed.FS
	//go:embed files/templates
	templatesFs embed.FS

	accounts   map[string]Account
	repository *Repository
	lndClient  *LndClient
)

func main() {
	var configFileName string

	flagSet := flag.NewFlagSet("LNURL Daemon", flag.ExitOnError)
	flagSet.StringVar(&configFileName, "config", "/etc/lnurld/config.yaml", "Path to a YAML config file.")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	config := loadConfig(configFileName)

	accounts = config.Accounts
	repository = newRepository(config.ThumbnailDir, config.DataDir)
	if client, err := newLndClient(config.Lnd); err != nil {
		log.Fatal(err)
	} else {
		lndClient = client
	}

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

	log.Fatal(lnurld.Run(config.Listen))
}

func loadTemplates(engine *gin.Engine, pattern string) {
	printer := message.NewPrinter(language.English)
	funcMap := template.FuncMap{
		"number": func(number any) string {
			return printer.Sprintf("%v", number)
		},
	}

	templates, err := template.New("templates").Funcs(funcMap).ParseFS(templatesFs, pattern)
	if err != nil {
		log.Fatal(err)
	}

	engine.SetHTMLTemplate(templates)
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
	invoice, err := lndClient.createInvoice(msats, comment, metadataHash[:])
	if err != nil {
		log.Println("Error creating invoice:", err)
		context.JSON(http.StatusInternalServerError, lnurl.ErrorResponse("Error creating invoice"))
		return
	}
	if err := repository.storePaymentHash(accountKey, invoice.paymentHash); err != nil {
		log.Println("Error storing payment hash:", err)
		context.JSON(http.StatusInternalServerError, lnurl.ErrorResponse("Error storing payment hash"))
		return
	}

	successMessage := "Thanks, payment received!"
	if account.Raffle != nil {
		successMessage = "Ticket " + invoice.ticket()
	}

	context.JSON(http.StatusOK, lnurl.LNURLPayValues{
		PR:            invoice.paymentRequest,
		SuccessAction: &lnurl.SuccessAction{Tag: "message", Message: successMessage},
		Routes:        []string{},
	})
}

func lnPayQrCodeHandler(context *gin.Context) {
	accountKey, _ := getAccount(context)
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

	pngData, err := qrcode.Encode("lightning:"+encodedLnUrl, qrcode.Medium, int(size))
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
	for accountKey := range accounts {
		accountKeys = append(accountKeys, accountKey)
	}
	sort.Strings(accountKeys)

	context.HTML(http.StatusOK, "accounts.gohtml", accountKeys)
}

func lnAccountHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}

	paymentHashes := repository.loadPaymentHashes(accountKey)

	var invoicesSettled int
	var totalSatsReceived int64
	for _, paymentHash := range paymentHashes {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.settled {
			invoicesSettled++
			totalSatsReceived += invoice.amount
		}
	}

	var lnAccountRaffle *LnAccountRaffle
	if raffle := account.Raffle; raffle != nil {
		lnAccountRaffle = &LnAccountRaffle{
			TicketPrice: raffle.TicketPrice,
			PrizesCount: len(raffle.Prizes),
		}
	}

	context.HTML(http.StatusOK, "account.gohtml", LnAccount{
		AccountKey:        accountKey,
		InvoicesIssued:    len(paymentHashes),
		InvoicesSettled:   invoicesSettled,
		TotalSatsReceived: totalSatsReceived,
		Raffle:            lnAccountRaffle,
	})
}

func lnAccountRaffleHandler(context *gin.Context) {
	accountKey, account := getAccount(context)
	if accountKey == "" {
		return
	}
	if account.Raffle == nil {
		context.String(http.StatusNotFound, "404 page not found")
		return
	}

	paymentHashes := repository.loadPaymentHashes(accountKey)
	rand.Shuffle(len(paymentHashes), func(i, j int) {
		paymentHashes[i], paymentHashes[j] = paymentHashes[j], paymentHashes[i]
	})

	var drawnTickets []string
	for _, paymentHash := range paymentHashes {
		invoice, err := lndClient.getInvoice(paymentHash)
		if err == nil && invoice.settled {
			drawnTickets = append(drawnTickets, invoice.ticket())
		}
	}

	if len(drawnTickets) == 0 {
		context.String(http.StatusForbidden, "403 forbidden")
		return
	}

	context.HTML(http.StatusOK, "raffle.gohtml", LnRaffle{
		Prizes:       account.Raffle.Prizes,
		DrawnTickets: drawnTickets,
	})
}

func getAccount(context *gin.Context) (string, *Account) {
	accountKey := context.Param("name")
	if account, accountExists := accounts[accountKey]; accountExists {
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
