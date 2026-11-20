package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/mail"
)

// runMailtest checks the SMTP settings (SMTP_HOST, SMTP_PORT, SMTP_USER,
// SMTP_PASSWORD, MAIL_FROM) without needing a database.
func runMailtest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mailtest", flag.ExitOnError)
	envFile := fs.String("env-file", "", "read variables from this file first (KEY=value lines, as Docker Compose env_file)")
	to := fs.String("to", "", "also send sample emails to this address")
	kind := fs.String("kind", "all", "samples to send with -to: login, digest or all")
	name := fs.String("service-name", "折光观察者", "service name shown in the samples")
	baseURL := fs.String("base-url", "", "site address in the samples (default $BASE_URL, else https://example.invalid)")
	fs.Parse(args)
	if *kind != "login" && *kind != "digest" && *kind != "all" {
		return fmt.Errorf("-kind must be login, digest or all, got %q", *kind)
	}

	if *envFile != "" {
		if err := loadEnvFile(*envFile); err != nil {
			return err
		}
	}
	port := 587
	if p := os.Getenv("SMTP_PORT"); p != "" {
		var err error
		if port, err = strconv.Atoi(p); err != nil {
			return fmt.Errorf("SMTP_PORT: %w", err)
		}
	}
	s, err := mail.NewSMTP(mail.SMTPConfig{
		Host: os.Getenv("SMTP_HOST"), Port: port, Username: os.Getenv("SMTP_USER"),
		Password: os.Getenv("SMTP_PASSWORD"), From: os.Getenv("MAIL_FROM"),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	start := time.Now()
	if err := s.Check(ctx); err != nil {
		return fmt.Errorf("SMTP login failed: %w", err)
	}
	fmt.Printf("SMTP login ok: %s:%d as %q (%s)\n", os.Getenv("SMTP_HOST"), port, os.Getenv("SMTP_USER"), time.Since(start).Round(time.Millisecond))
	if *to == "" {
		return nil
	}
	addr, err := auth.NormalizeEmail(*to)
	if err != nil {
		return err
	}

	site := strings.TrimRight(*baseURL, "/")
	if site == "" {
		site = strings.TrimRight(os.Getenv("BASE_URL"), "/")
	}
	if site == "" {
		site = "https://example.invalid"
	}
	login, err := mail.Render("login", addr, mail.LoginData{Code: "000000", ValidMinutes: 10, ServiceName: *name, ServiceURL: site})
	if err != nil {
		return err
	}
	digest, err := mail.Render("digest", addr, mail.DigestData{
		Deleted: 2, ServiceName: *name, ServiceURL: site,
		Items: []mail.DigestItem{
			{Title: "（测试）一篇被删除的文章", AccountName: "测试公众号", Reason: "此内容因违规无法查看", Link: site + "/watches/test"},
			{Title: "（测试）另一篇被删除的文章", AccountName: "测试公众号", Reason: "该内容已被发布者删除", Link: site + "/watches/test"},
		},
	})
	if err != nil {
		return err
	}
	var msgs []mail.Message
	if *kind != "digest" {
		msgs = append(msgs, login)
	}
	if *kind != "login" {
		msgs = append(msgs, digest)
	}
	for _, m := range msgs {
		m.Subject = "[测试] " + m.Subject
		id, err := s.Send(ctx, m)
		if err != nil {
			return fmt.Errorf("send %q: %w", m.Subject, err)
		}
		fmt.Printf("sent %q, Message-ID %s\n", m.Subject, id)
	}
	return nil
}

// loadEnvFile sets KEY=value lines from path, without shell quoting rules
// (values may contain spaces and <>), like Docker Compose env_file. Variables
// already set in the environment win.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: not KEY=value", path, n)
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}
