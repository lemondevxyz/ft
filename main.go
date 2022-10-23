package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/lemondevxyz/ft/internal/web"
	"github.com/spf13/afero"
)

// @Version 0.1.0
// @Title ft
// @Description the API definition for ft, the remote file browser
// @ContactName Ahmed Mazen
// @ContactEmail lemon@lemondev.xyz
// @ContactURL https://lemondev.xyz
// @LicenseName GPL V3
// @LicenseURL https://www.gnu.org/licenses/gpl-3.0.en.html

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
