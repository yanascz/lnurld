package main

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"html/template"
	"log"
	"time"
)

func loadTemplates(engine *gin.Engine, pattern string) {
	printer := message.NewPrinter(language.English)
	funcMap := template.FuncMap{
		"number": func(number any) string {
			return printer.Sprintf("%v", number)
		},
		"decimal": func(number any) string {
			return printer.Sprintf("%.2f", number)
		},
		"date": func(date time.Time) string {
			return date.Format("02/01/2006")
		},
	}

	templates, err := template.New("templates").Funcs(funcMap).ParseFS(templatesFs, pattern)
	if err != nil {
		log.Fatal(err)
	}

	engine.SetHTMLTemplate(templates)
}
