package main

import (
	"crypto/rand"
	"github.com/gin-gonic/gin"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"strings"
	"time"
)

type Config struct {
	Listen        string
	ThumbnailDir  string `yaml:"thumbnail-dir"`
	DataDir       string `yaml:"data-dir"`
	Lnd           LndConfig
	Credentials   gin.Accounts
	AccessControl map[string][]string `yaml:"access-control"`
	Accounts      map[string]Account
	Events        map[string]Event
}

func (config *Config) getCookieKey() []byte {
	cookieKeyFileName := config.DataDir + ".cookie"
	cookieKey, err := os.ReadFile(cookieKeyFileName)
	if err == nil {
		return cookieKey
	}

	cookieKey = make([]byte, 32)
	if _, err := rand.Read(cookieKey); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(cookieKeyFileName, cookieKey, 0400); err != nil {
		log.Fatal(err)
	}

	return cookieKey
}

func (config *Config) isUserAuthorized(context *gin.Context, accountKey string) bool {
	user := context.GetString(gin.AuthUserKey)
	allowedAccounts, accessRestricted := config.AccessControl[user]

	return !accessRestricted || slices.Contains(allowedAccounts, accountKey)
}

type Account struct {
	Currency       Currency `yaml:"currency"`
	MaxSendable    uint32   `yaml:"max-sendable"`
	MinSendable    uint32   `yaml:"min-sendable"`
	Description    string
	Thumbnail      string
	IsAlsoEmail    bool   `yaml:"is-also-email"`
	CommentAllowed uint16 `yaml:"comment-allowed"`
	Archivable     bool
	Raffle         *Raffle
}

func (account *Account) getCurrency() Currency {
	if currency := account.Currency; currency != "" {
		return currency
	}
	return EUR
}

func (account *Account) getMinSendable() int64 {
	if raffle := account.Raffle; raffle != nil {
		return msats(raffle.TicketPrice)
	}
	return msats(account.MinSendable)
}

func (account *Account) getMaxSendable() int64 {
	if raffle := account.Raffle; raffle != nil {
		return msats(raffle.TicketPrice)
	}
	return msats(account.MaxSendable)
}

func msats(sats uint32) int64 {
	return int64(sats) * 1000
}

type Raffle struct {
	TicketPrice uint32 `yaml:"ticket-price"`
	Prizes      []map[string]uint8
}

func (raffle *Raffle) getPrizesCount() int {
	var prizesCount int
	for _, entry := range raffle.Prizes {
		for _, quantity := range entry {
			prizesCount += int(quantity)
			break
		}
	}
	return prizesCount
}

type Event struct {
	Title       string
	DateTime    time.Time
	Location    string
	Capacity    uint16
	Description string
}

func loadConfig(configFileName string) *Config {
	config := Config{
		Listen:       "127.0.0.1:8088",
		ThumbnailDir: "/etc/lnurld/thumbnails",
		DataDir:      "/var/lib/lnurld",
		Lnd: LndConfig{
			Address:      "127.0.0.1:10009",
			CertFile:     "/var/lib/lnd/tls.cert",
			MacaroonFile: "/var/lib/lnd/data/chain/bitcoin/mainnet/invoice.macaroon",
		},
	}

	configData, err := os.ReadFile(configFileName)
	if err != nil {
		log.Fatal(err)
	}
	if err := yaml.Unmarshal(configData, &config); err != nil {
		log.Fatal(err)
	}

	if !strings.HasSuffix(config.ThumbnailDir, pathSeparator) {
		config.ThumbnailDir += pathSeparator
	}
	if !strings.HasSuffix(config.DataDir, pathSeparator) {
		config.DataDir += pathSeparator
	}

	validateAccessControl(&config)
	for accountKey, account := range config.Accounts {
		validateAccount(accountKey, &account)
	}
	for eventKey, event := range config.Events {
		validateEvent(eventKey, &event)
	}

	return &config
}

func validateAccessControl(config *Config) {
	for user, allowedAccounts := range config.AccessControl {
		if _, userExists := config.Credentials[user]; !userExists {
			log.Fatal("Unknown user in property access-control: ", user)
		}
		for _, accountKey := range allowedAccounts {
			if _, accountExists := config.Accounts[accountKey]; !accountExists {
				log.Fatal("Unknown account in property access-control.", user, ": ", accountKey)
			}
		}
	}
}

func validateAccount(accountKey string, account *Account) {
	if !slices.Contains(supportedCurrencies(), account.getCurrency()) {
		logInvalidAccountValue(accountKey, "currency", account.Currency)
	}
	if raffle := account.Raffle; raffle == nil {
		if account.MaxSendable < 1 {
			logInvalidAccountValue(accountKey, "max-sendable", account.MaxSendable)
		}
		if account.MinSendable < 1 || account.MinSendable > account.MaxSendable {
			logInvalidAccountValue(accountKey, "min-sendable", account.MinSendable)
		}
	} else {
		if account.MaxSendable > 0 {
			logInvalidAccountConfig(accountKey, "max-sendable")
		}
		if account.MinSendable > 0 {
			logInvalidAccountConfig(accountKey, "min-sendable")
		}
		if ticketPrice := raffle.TicketPrice; ticketPrice < 1 {
			logInvalidAccountValue(accountKey, "raffle.ticket-price", ticketPrice)
		}
		if prizes := raffle.Prizes; len(prizes) == 0 {
			logInvalidAccountValue(accountKey, "raffle.prizes", prizes)
		}
	}
	if strings.TrimSpace(account.Description) == "" {
		logInvalidAccountValue(accountKey, "description", account.Description)
	}
	if account.CommentAllowed > 2000 {
		logInvalidAccountValue(accountKey, "comment-allowed", account.CommentAllowed)
	}
}

func logInvalidAccountValue(accountKey string, property string, value any) {
	log.Fatal("Invalid config value accounts.", accountKey, ".", property, ": ", value)
}

func logInvalidAccountConfig(accountKey string, property string) {
	log.Fatal("Cannot set accounts.", accountKey, ".", property, " when raffle is enabled")
}

func validateEvent(eventKey string, event *Event) {
	if strings.TrimSpace(event.Title) == "" {
		logMissingEventValue(eventKey, "title")
	}
	if event.DateTime.IsZero() {
		logMissingEventValue(eventKey, "datetime")
	}
	if strings.TrimSpace(event.Location) == "" {
		logMissingEventValue(eventKey, "location")
	}
	if strings.TrimSpace(event.Description) == "" {
		logMissingEventValue(eventKey, "description")
	}
}

func logMissingEventValue(eventKey string, property string) {
	log.Fatal("Missing config value events.", eventKey, ".", property)
}
