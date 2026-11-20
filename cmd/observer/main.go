// Command observer is the Observer service and its tooling.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/MamaShip/Observer/internal/api"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `observer - WeChat article backup service

Usage:
  observer serve                     run the HTTP API and background workers
  observer migrate [up|status]       apply or list database migrations
  observer healthcheck [-url URL]    exit 0 if the local service is healthy (for Docker)
  observer admin stats               print service counters
  observer probe [flags] <url>...    fetch article links and report what WeChat returns
  observer netcheck [flags]          measure network paths to WeChat, Wasabi and SMTP
  observer mailtest [-to ADDR]       log in to the SMTP server; with -to, send sample emails
  observer openapi                   print the API's OpenAPI document (JSON)
  observer version

Configuration comes from environment variables; see plan.md §13.3.
Run "observer <command> -h" for command flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(ctx, args)
	case "migrate":
		err = runMigrate(ctx, args)
	case "healthcheck":
		err = runHealthcheck(ctx, args)
	case "admin":
		err = runAdmin(ctx, args)
	case "probe":
		err = runProbe(ctx, args)
	case "netcheck":
		err = runNetcheck(ctx, args)
	case "mailtest":
		err = runMailtest(ctx, args)
	case "openapi":
		var b []byte
		if b, err = api.Spec().MarshalJSON(); err == nil {
			var out bytes.Buffer
			if err = json.Indent(&out, b, "", "  "); err == nil {
				out.WriteByte('\n')
				_, err = os.Stdout.Write(out.Bytes())
			}
		}
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
