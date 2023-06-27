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

const (
	pathSeparator = string(os.PathSeparator)
	eventsDirName = "events" + pathSeparator
	fileExtension = ".csv"
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
	_ = os.Mkdir(dataDir+eventsDirName, 0700)

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
	return storeValue(accountStorageFileName(repository, accountKey), paymentHash)
}

func (repository *Repository) loadPaymentHashes(accountKey string) []string {
	return loadValues(accountStorageFileName(repository, accountKey))
}

func (repository *Repository) archiveStorageFile(accountKey string) error {
	storageFileName := accountStorageFileName(repository, accountKey)
	archiveFileName := storageFileName + "." + time.Now().Format("20060102150405")

	return os.Rename(storageFileName, archiveFileName)
}

func accountStorageFileName(repository *Repository, accountKey string) string {
	return repository.dataDir + accountKey + fileExtension
}

func (repository *Repository) storeIdentity(eventKey string, identity string) error {
	return storeValue(eventStorageFileName(repository, eventKey), identity)
}

func (repository *Repository) loadIdentities(eventKey string) []string {
	return loadValues(eventStorageFileName(repository, eventKey))
}

func eventStorageFileName(repository *Repository, eventKey string) string {
	return repository.dataDir + eventsDirName + eventKey + fileExtension
}

func storeValue(storageFileName string, value string) error {
	storage, err := os.OpenFile(storageFileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer storage.Close()

	line := value + "\n"
	if _, err = storage.WriteString(line); err != nil {
		return err
	}

	return nil
}

func loadValues(storageFileName string) []string {
	storage, err := os.OpenFile(storageFileName, os.O_RDONLY, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Println("Error loading values:", err)
		}
		return []string{}
	}
	defer storage.Close()

	var values []string
	for scanner := bufio.NewScanner(storage); scanner.Scan(); {
		values = append(values, scanner.Text())
	}

	return values
}
