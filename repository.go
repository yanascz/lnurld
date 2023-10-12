package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/mr-tron/base58"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	pathSeparator  = string(os.PathSeparator)
	eventsDirName  = "events" + pathSeparator
	rafflesDirName = "raffles" + pathSeparator
	dataFileName   = "data.json"
	csvExtension   = ".csv"
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
	_ = os.Mkdir(dataDir+eventsDirName, 0755)
	_ = os.Mkdir(dataDir+rafflesDirName, 0755)

	return &Repository{
		thumbnailDir: thumbnailDir,
		dataDir:      dataDir,
	}
}

func (repository *Repository) getThumbnail(fileName string) (*Thumbnail, error) {
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

func (repository *Repository) addAccountPaymentHash(accountKey string, paymentHash string) error {
	return appendValue(accountPaymentHashesFileName(repository, accountKey), paymentHash)
}

func (repository *Repository) getAccountPaymentHashes(accountKey string) []string {
	return readValues(accountPaymentHashesFileName(repository, accountKey))
}

func (repository *Repository) archiveAccountPaymentHashes(accountKey string) error {
	dataFileName := accountPaymentHashesFileName(repository, accountKey)
	archiveFileName := dataFileName + "." + time.Now().Format("20060102150405")

	return os.Rename(dataFileName, archiveFileName)
}

func (repository *Repository) createEvent(event *Event) error {
	eventId, err := randomId()
	if err != nil {
		return err
	}

	err = os.Mkdir(eventDirName(repository, eventId), 0755)
	if err != nil {
		return err
	}
	event.Id = eventId

	return writeObject(eventDataFileName(repository, eventId), event)
}

func (repository *Repository) getEvent(eventId string) *Event {
	var event Event
	if readObject(eventDataFileName(repository, eventId), &event) != nil {
		return nil
	}
	event.Id = eventId

	return &event
}

func (repository *Repository) getEvents() []*Event {
	var events []*Event
	for _, dirEntry := range readDirEntries(repository.dataDir + eventsDirName) {
		if event := repository.getEvent(dirEntry.Name()); event != nil {
			events = append(events, event)
		}
	}

	return events
}

func (repository *Repository) updateEvent(event *Event) error {
	return writeObject(eventDataFileName(repository, event.Id), event)
}

func (repository *Repository) addEventAttendee(event *Event, identity string) error {
	return appendValue(eventAttendeesFileName(repository, event.Id), identity)
}

func (repository *Repository) getEventAttendees(event *Event) []string {
	return readValues(eventAttendeesFileName(repository, event.Id))
}

func (repository *Repository) createRaffle(raffle *Raffle) error {
	raffleId, err := randomId()
	if err != nil {
		return err
	}

	err = os.Mkdir(raffleDirName(repository, raffleId), 0755)
	if err != nil {
		return err
	}
	raffle.Id = raffleId

	return writeObject(raffleDataFileName(repository, raffleId), raffle)
}

func (repository *Repository) getRaffle(raffleId string) *Raffle {
	var raffle Raffle
	if readObject(raffleDataFileName(repository, raffleId), &raffle) != nil {
		return nil
	}
	raffle.Id = raffleId

	return &raffle
}

func (repository *Repository) getRaffles() []*Raffle {
	var raffles []*Raffle
	for _, dirEntry := range readDirEntries(repository.dataDir + rafflesDirName) {
		if event := repository.getRaffle(dirEntry.Name()); event != nil {
			raffles = append(raffles, event)
		}
	}

	return raffles
}

func (repository *Repository) updateRaffle(raffle *Raffle) error {
	return writeObject(raffleDataFileName(repository, raffle.Id), raffle)
}

func (repository *Repository) addRafflePaymentHash(raffle *Raffle, paymentHash string) error {
	return appendValue(raffleTicketsFileName(repository, raffle.Id), paymentHash)
}

func (repository *Repository) getRafflePaymentHashes(raffle *Raffle) []string {
	return readValues(raffleTicketsFileName(repository, raffle.Id))
}

func accountPaymentHashesFileName(repository *Repository, accountKey string) string {
	return repository.dataDir + accountKey + csvExtension
}

func eventDirName(repository *Repository, eventId string) string {
	return repository.dataDir + eventsDirName + eventId
}

func eventDataFileName(repository *Repository, eventId string) string {
	return eventDirName(repository, eventId) + pathSeparator + dataFileName
}

func eventAttendeesFileName(repository *Repository, eventId string) string {
	return eventDirName(repository, eventId) + pathSeparator + "attendees" + csvExtension
}

func raffleDirName(repository *Repository, raffleId string) string {
	return repository.dataDir + rafflesDirName + raffleId
}

func raffleDataFileName(repository *Repository, raffleId string) string {
	return raffleDirName(repository, raffleId) + pathSeparator + dataFileName
}

func raffleTicketsFileName(repository *Repository, raffleId string) string {
	return raffleDirName(repository, raffleId) + pathSeparator + "tickets" + csvExtension
}

func randomId() (string, error) {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}

	return base58.Encode(random), nil
}

func writeObject(fileName string, object any) error {
	jsonData, err := json.Marshal(object)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err = file.Write(jsonData); err != nil {
		return err
	}

	return nil
}

func readObject(fileName string, object any) error {
	fileBytes, err := os.ReadFile(fileName)
	if err != nil {
		return err
	}

	return json.Unmarshal(fileBytes, object)
}

func readDirEntries(dirName string) []os.DirEntry {
	dirEntries, err := os.ReadDir(dirName)
	if err != nil {
		log.Println("error reading directory:", err)
		return []os.DirEntry{}
	}

	return dirEntries
}

func appendValue(fileName string, value string) error {
	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	line := value + "\n"
	if _, err = file.WriteString(line); err != nil {
		return err
	}

	return nil
}

func readValues(fileName string) []string {
	file, err := os.OpenFile(fileName, os.O_RDONLY, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Println("error reading values:", err)
		}
		return []string{}
	}
	defer file.Close()

	var values []string
	for scanner := bufio.NewScanner(file); scanner.Scan(); {
		values = append(values, scanner.Text())
	}

	return values
}
