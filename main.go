package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/lemondevxyz/ft/internal/web"
	"github.com/spf13/afero"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Please provider a directory to use")
		os.Exit(1)
	}

	fs := afero.NewBasePathFs(afero.NewOsFs(), os.Args[1])
	addr := os.Getenv("FT_ADDR")
	if len(addr) == 0 {
		addr = ":8080"
	}

	serv, err := web.NewWebInstance(addr, fs, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Web instance err'd out: %s\n", err.Error())
	}
	serv.Start()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c

	serv.Stop()
}
