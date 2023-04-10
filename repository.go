package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
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

func (repository *Repository) storePaymentHash(accountKey string, paymentHash string) error {
	storageFileName := repository.accountStorageFileName(accountKey)
	storage, err := os.OpenFile(storageFileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer storage.Close()

	line := paymentHash + "\n"
	if _, err = storage.WriteString(line); err != nil {
		return err
	}

	return nil
}

func (repository *Repository) loadPaymentHashes(accountKey string) []string {
	storageFileName := repository.accountStorageFileName(accountKey)
	storage, err := os.OpenFile(storageFileName, os.O_RDONLY, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Println("Error loading payment hashes:", err)
		}
		return []string{}
	}
	defer storage.Close()

	var paymentHashes []string
	for scanner := bufio.NewScanner(storage); scanner.Scan(); {
		paymentHashes = append(paymentHashes, scanner.Text())
	}

	return paymentHashes
}

func (repository *Repository) archiveStorageFile(accountKey string) error {
	storageFileName := repository.accountStorageFileName(accountKey)
	archiveFileName := storageFileName + "." + time.Now().Format("20060102150405")

	return os.Rename(storageFileName, archiveFileName)
}

func (repository *Repository) accountStorageFileName(accountKey string) string {
	return repository.dataDir + accountKey + ".csv"
}
