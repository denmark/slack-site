package main

import (
	"log"

	"github.com/denmark/slack-site/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
