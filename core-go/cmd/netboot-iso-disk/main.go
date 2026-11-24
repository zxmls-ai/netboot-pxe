package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/sky22333/android-netboot/core-go/internal/isodisk"
)

func main() {
	address := flag.String("listen", "", "IPv4 address and port to bind")
	catalog := flag.String("catalog", "", "path to the PXE ISO catalog")
	flag.Parse()
	if *address == "" || *catalog == "" {
		flag.Usage()
		os.Exit(2)
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host).To4() == nil || host == "0.0.0.0" {
		log.Fatal("listen must name a specific IPv4 address and port")
	}
	ln, err := net.Listen("tcp4", *address)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := isodisk.Serve(ctx, ln, *catalog, "", 8); err != nil {
		log.Fatal(err)
	}
}
