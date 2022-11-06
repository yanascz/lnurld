package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

type Thumbnail struct {
	bytes []byte
	ext   string
}

type Repository struct {
	thumbnailDir string
	dataDir      string
}

func newRepository(thumbnailDir string, dataDir string) *Repository {
	return &Repository{
		thumbnailDir: thumbnailDir,
		dataDir:      dataDir,
	}
}

func (repository *Repository) loadThumbnail(fileName string) (*Thumbnail, error) {
	thumbnailData, err := os.ReadFile(repository.thumbnailDir + fileName)
	if err != nil {
		return nil, err
	}

	mimeType := http.DetectContentType(thumbnailData)
	if mimeType != "image/png" && mimeType != "image/jpeg" {
		return nil, fmt.Errorf("unsupported MIME type: %s", mimeType)
	}

	return &Thumbnail{
		bytes: thumbnailData,
		ext:   strings.TrimPrefix(mimeType, "image/"),
	}, nil
}

func (repository *Repository) storePaymentHash(accountKey string, paymentHash []byte) error {
	fileName := repository.accountStorageFileName(accountKey)
	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	line := hex.EncodeToString(paymentHash) + "\n"
	if _, err = file.WriteString(line); err != nil {
		return err
	}

	return nil
}

func (repository *Repository) loadPaymentHashes(accountKey string) []string {
	fileName := repository.accountStorageFileName(accountKey)
	file, err := os.OpenFile(fileName, os.O_RDONLY, 0)
	if err != nil {
		log.Println("Error loading payment hashes:", err)
		return []string{}
	}
	defer file.Close()

	var paymentHashes []string
	for scanner := bufio.NewScanner(file); scanner.Scan(); {
		paymentHashes = append(paymentHashes, scanner.Text())
	}

	return paymentHashes
}

func (repository *Repository) accountStorageFileName(accountKey string) string {
	return repository.dataDir + accountKey + ".csv"
}
