// Command observer is the Observer service and its tooling.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MamaShip/Observer/internal/netcheck"
	"github.com/MamaShip/Observer/internal/probe"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `observer - WeChat article backup service

Usage:
  observer probe [flags] <url>...    fetch article links and report what WeChat returns
  observer netcheck [flags]          measure network paths to WeChat, Wasabi and SMTP
  observer version

Run "observer <command> -h" for command flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "probe":
		err = runProbe(ctx, os.Args[2:])
	case "netcheck":
		err = runNetcheck(ctx, os.Args[2:])
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

func runProbe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	cfg := probe.Config{}
	fs.StringVar(&cfg.UserAgent, "ua", "ios", "User-Agent preset (ios, android, desktop) or a full UA string")
	fs.IntVar(&cfg.Repeat, "repeat", 1, "attempts per URL (rate-limit test)")
	fs.DurationVar(&cfg.Interval, "interval", 3*time.Second, "minimum gap between page requests")
	fs.BoolVar(&cfg.Full, "full", false, "also download the whole page (as a snapshot would)")
	fs.IntVar(&cfg.Images, "images", 0, "download up to N images per alive article")
	fs.BoolVar(&cfg.RefererTest, "referer-test", false, "also download each image without Referer")
	fs.StringVar(&cfg.FixtureDir, "save-fixture", "", "save fetched pages (gzip, secrets redacted) to this directory")
	fs.IntVar(&cfg.Window, "window", 16<<10, "HTTP/2 receive window in bytes (small windows make early stops save traffic)")
	fs.BoolVar(&cfg.JSON, "json", false, "print JSON lines")
	fs.BoolVar(&cfg.Head, "head", false, "experimental: periodic-style fetch reads only the page head (~20 KB)")
	file := fs.String("file", "", "read URLs from file (one per line, # comments allowed)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: observer probe [flags] <url>...\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	cfg.URLs = fs.Args()
	if *file != "" {
		urls, err := readLines(*file)
		if err != nil {
			return err
		}
		cfg.URLs = append(cfg.URLs, urls...)
	}
	if len(cfg.URLs) == 0 {
		fs.Usage()
		os.Exit(2)
	}
	if cfg.Interval < time.Second {
		return fmt.Errorf("interval below 1s is not allowed, to avoid hammering WeChat")
	}
	return probe.Run(ctx, cfg)
}

func runNetcheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("netcheck", flag.ExitOnError)
	regions := fs.String("wasabi-regions", strings.Join(netcheck.DefaultWasabiRegions, ","), `comma-separated Wasabi regions, or "all"`)
	smtpHost := fs.String("smtp-host", "smtp.resend.com", `SMTP host to test ports 25/587/465 against ("" to skip)`)
	samples := fs.Int("samples", 3, "samples per target")
	s3 := fs.Bool("s3", false, "also test S3 throughput using S3_ENDPOINT, S3_REGION, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
	fs.Parse(args)

	var rs []string
	for _, r := range strings.Split(*regions, ",") {
		if r = strings.TrimSpace(r); r != "" {
			rs = append(rs, r)
		}
	}
	if *regions == "all" {
		rs = netcheck.WasabiRegions
	}
	netcheck.Run(ctx, os.Stdout, netcheck.Targets(rs, *smtpHost), *samples)

	if *s3 {
		c, ok := netcheck.S3FromEnv()
		if !ok {
			return fmt.Errorf("-s3 needs S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY and S3_SECRET_KEY")
		}
		fmt.Println()
		return netcheck.RunS3(ctx, os.Stdout, c)
	}
	return nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}
