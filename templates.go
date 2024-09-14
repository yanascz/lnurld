package main

import (
	"embed"
	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"html/template"
	"log"
	"time"
)

//go:embed files/templates
var templatesFs embed.FS

func loadTemplates(engine *gin.Engine, pattern string) {
	printer := message.NewPrinter(language.English)
	location := time.Now().Location()
	funcMap := template.FuncMap{
		"number": func(number any, args ...string) template.HTML {
			numberFormatted := printer.Sprintf("%d", number)
			if len(args) == 0 {
				return template.HTML(numberFormatted)
			}
			unit := args[0]
			if numberFormatted != "1" {
				unit += "s"
			}
			return htmlValue(numberFormatted, unit)
		},
		"currency": func(amount any, currency Currency) template.HTML {
			return htmlValue(printer.Sprintf("%.2f", amount), currencyCode(currency))
		},
		"currencyCode": currencyCode,
		"date": func(date time.Time) string {
			return date.In(location).Format("02/01/2006")
		},
		"time": func(date time.Time) string {
			return date.In(location).Format("15:04")
		},
		"datetime": func(date time.Time) string {
			return date.In(location).Format("02/01/2006 15:04")
		},
	}

	defaultTemplate := template.New("templates").Funcs(funcMap)
	completeTemplate, err := defaultTemplate.ParseFS(templatesFs, pattern)
	if err != nil {
		log.Fatal(err)
	}

	engine.SetHTMLTemplate(completeTemplate)
}

func htmlValue(value string, unit string) template.HTML {
	return template.HTML("<strong>" + value + "</strong> " + unit)
}
