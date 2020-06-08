package main

import (
	"context"
	"log"

	"github.com/ericnorris/bulldozer/internal/cmd/bulldozer"

	"github.com/alecthomas/kong"
)

func main() {
	cliContext := kong.Parse(&bulldozer.Cmd{}, kong.UsageOnError())

	cliContext.BindTo(context.Background(), (*context.Context)(nil))

	if err := cliContext.Run(); err != nil {
		log.Fatalf("[FATAL] %s", err)
	}
}
