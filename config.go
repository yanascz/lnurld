package main

import (
	"crypto/rand"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"slices"
	"strings"
	"time"
)

type Config struct {
	Listen         string
	ThumbnailDir   string `yaml:"thumbnail-dir"`
	DataDir        string `yaml:"data-dir"`
	Lnd            LndConfig
	Nostr          NostrConfig
	Credentials    map[UserKey]string
	Administrators []UserKey
	AccessControl  map[UserKey][]AccountKey `yaml:"access-control"`
	Thumbnails     map[UserKey]string       `yaml:"thumbnails"`
	Accounts       map[AccountKey]Account
	Authentication AuthenticationConfig
	Withdrawal     WithdrawalConfig
}

func (config *Config) cookieKey() []byte {
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

type UserKey string

type AccountKey string

type Account struct {
	Currency       Currency `yaml:"currency"`
	MinSendable    uint32   `yaml:"min-sendable"`
	MaxSendable    uint32   `yaml:"max-sendable"`
	Description    string
	Thumbnail      string
	IsAlsoEmail    bool   `yaml:"is-also-email"`
	CommentAllowed uint16 `yaml:"comment-allowed"`
	AllowsNostr    bool   `yaml:"allows-nostr"`
	SuccessMessage string `yaml:"success-message"`
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

func msats[T int | uint32 | int64](sats T) int64 {
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
			MacaroonFile: "/var/lib/lnd/data/chain/bitcoin/mainnet/invoices.macaroon",
			CacheSize:    1024,
		},
		Authentication: AuthenticationConfig{
			RequestExpiry: 90 * time.Second,
		},
		Withdrawal: WithdrawalConfig{
			RequestExpiry: 90 * time.Second,
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
	validateThumbnails(&config)
	validateAccounts(&config)

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

func validateThumbnails(config *Config) {
	for user, _ := range config.Thumbnails {
		if _, userExists := config.Credentials[user]; !userExists {
			log.Fatal("Unknown user in property thumbnails: ", user)
		}
	}
}

func validateAccounts(config *Config) {
	for accountKey, account := range config.Accounts {
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
		if len(account.SuccessMessage) > 144 {
			logInvalidAccountValue(accountKey, "success-message", account.SuccessMessage)
		}
	}
}

func logInvalidAccountValue(accountKey AccountKey, property string, value any) {
	log.Fatal("Invalid config value accounts.", accountKey, ".", property, ": ", value)
}
