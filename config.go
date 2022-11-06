package main

import (
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"strings"
)

type Config struct {
	Listen       string
	ThumbnailDir string `yaml:"thumbnail-dir"`
	DataDir      string `yaml:"data-dir"`
	Lnd          LndConfig
	Credentials  gin.Accounts
	Accounts     map[string]Account
}

type Account struct {
	MaxSendable    uint32 `yaml:"max-sendable"`
	MinSendable    uint32 `yaml:"min-sendable"`
	Description    string
	Thumbnail      string
	IsAlsoEmail    bool   `yaml:"is-also-email"`
	CommentAllowed uint16 `yaml:"comment-allowed"`
	Raffle         *Raffle
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
	Prizes      []string
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

	const pathSeparator = string(os.PathSeparator)
	if !strings.HasSuffix(config.ThumbnailDir, pathSeparator) {
		config.ThumbnailDir += pathSeparator
	}
	if !strings.HasSuffix(config.DataDir, pathSeparator) {
		config.DataDir += pathSeparator
	}

	for accountKey, account := range config.Accounts {
		validateAccount(accountKey, &account)
	}

	return &config
}

func validateAccount(accountKey string, account *Account) {
	if raffle := account.Raffle; raffle == nil {
		if account.MaxSendable < 1 {
			logInvalidAccountValue(accountKey, "max-sendable", account.MaxSendable)
		}
		if account.MinSendable < 1 || account.MinSendable > account.MaxSendable {
			logInvalidAccountValue(accountKey, "min-sendable", account.MinSendable)
		}
	} else {
		if account.MaxSendable > 0 {
			logInvalidAccountConfig(accountKey, "max-sendable", "raffle")
		}
		if account.MinSendable > 0 {
			logInvalidAccountConfig(accountKey, "min-sendable", "raffle")
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

func logInvalidAccountConfig(accountKey string, property string, feature string) {
	log.Fatal("Cannot set accounts.", accountKey, ".", property, " when ", feature, " is enabled")
}
