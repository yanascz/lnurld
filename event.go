package main

import (
	"sort"
	"strings"
	"time"
)

type Event struct {
	Id          string        `json:"-"`
	Owner       string        `json:"owner"`
	Title       string        `json:"title" binding:"min=1,max=50"`
	Start       time.Time     `json:"start"`
	End         time.Time     `json:"end" binding:"gtfield=Start"`
	Location    EventLocation `json:"location" binding:"required"`
	Capacity    uint16        `json:"capacity" binding:"min=1,max=1000"`
	Description string        `json:"description" binding:"min=1,max=500"`
}

type EventLocation struct {
	Name string `json:"name" binding:"min=1,max=50"`
	Url  string `json:"url" binding:"url,max=100"`
}

func (event *Event) isInPast() bool {
	return event.End.Before(time.Now())
}

func sortEvents(events []*Event) []*Event {
	sort.Slice(events, func(i, j int) bool {
		return events[i].Start.Before(events[j].Start)
	})
	return events
}

func sortPastEvents(events []*Event) []*Event {
	sort.Slice(events, func(i, j int) bool {
		return events[i].End.After(events[j].End)
	})
	return events
}

func iCalendarEvent(event *Event, host string) string {
	return "BEGIN:VCALENDAR\n" +
		"VERSION:2.0\n" +
		"PRODID:-//yanascz//NONSGML LNURL Daemon//EN\n" +
		"BEGIN:VEVENT\n" +
		"UID:event-" + event.Id + "@" + host + "\n" +
		"DTSTAMP:" + iCalendarDateTime(time.Now()) + "\n" +
		"SUMMARY:" + iCalendarText(event.Title) + "\n" +
		"DTSTART:" + iCalendarDateTime(event.Start) + "\n" +
		"DTEND:" + iCalendarDateTime(event.End) + "\n" +
		"LOCATION:" + iCalendarText(event.Location.Name) + "\n" +
		"DESCRIPTION:" + iCalendarText(event.Description) + "\n" +
		"END:VEVENT\n" +
		"END:VCALENDAR\n"
}

var iCalendarEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `;`, `\;`, `,`, `\,`)

func iCalendarText(text string) string {
	return iCalendarEscaper.Replace(text)
}

func iCalendarDateTime(dateTime time.Time) string {
	return dateTime.UTC().Format("20060102T150405Z07")
}