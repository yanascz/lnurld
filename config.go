package main

import (
	"crypto/rand"
	"github.com/gin-gonic/gin"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"strings"
)

type Config struct {
	Listen         string
	ThumbnailDir   string `yaml:"thumbnail-dir"`
	DataDir        string `yaml:"data-dir"`
	Lnd            LndConfig
	Credentials    gin.Accounts
	Administrators []string
	AccessControl  map[string][]string `yaml:"access-control"`
	Accounts       map[string]Account
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

type Account struct {
	Currency       Currency `yaml:"currency"`
	MaxSendable    uint32   `yaml:"max-sendable"`
	MinSendable    uint32   `yaml:"min-sendable"`
	Description    string
	Thumbnail      string
	IsAlsoEmail    bool   `yaml:"is-also-email"`
	CommentAllowed uint16 `yaml:"comment-allowed"`
	Archivable     bool
}

func (account *Account) getCurrency() Currency {
	if currency := account.Currency; currency != "" {
		return currency
	}
	return EUR
}

func (account *Account) getMinSendable() int64 {
	return msats(account.MinSendable)
}

func (account *Account) getMaxSendable() int64 {
	return msats(account.MaxSendable)
}

func msats(sats uint32) int64 {
	return int64(sats) * 1000
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

	validateAdministrators(&config)
	validateAccessControl(&config)
	for accountKey, account := range config.Accounts {
		validateAccount(accountKey, &account)
	}

	return &config
}

func validateAdministrators(config *Config) {
	for _, user := range config.Administrators {
		if _, userExists := config.Credentials[user]; !userExists {
			log.Fatal("Unknown user in property administrators: ", user)
		}
	}
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
	if account.MaxSendable < 1 {
		logInvalidAccountValue(accountKey, "max-sendable", account.MaxSendable)
	}
	if account.MinSendable < 1 || account.MinSendable > account.MaxSendable {
		logInvalidAccountValue(accountKey, "min-sendable", account.MinSendable)
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
