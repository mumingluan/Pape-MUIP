package main

import (
	"flag"
	"log"

	"pape-muip/internal/app"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()
	if err := app.Run(*configPath); err != nil {
		log.Fatal(err)
	}
}
