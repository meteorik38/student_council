package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"unicode/utf8"

	"kan-db/internal/auth"
	"kan-db/internal/config"
	"kan-db/internal/httpapi"
	"kan-db/internal/migrations"
	"kan-db/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "healthcheck" {
		return healthcheck()
	}
	if command != "serve" && command != "migrate" && command != "create-admin" {
		return fmt.Errorf("usage: server [serve|migrate|create-admin|healthcheck]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	db, err := store.Open(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	if cfg.AutoMigrate || command == "migrate" {
		if err = migrations.Apply(startup, db.Pool); err != nil {
			return err
		}
	}
	if command == "migrate" {
		slog.Info("database migrations applied")
		return nil
	}
	if command == "create-admin" {
		return createAdmin(startup, db)
	}
	handler, err := httpapi.New(db, cfg.Secret, cfg.AllowLegacyPasswords)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	slog.Info("HTTP server listening", "address", cfg.Address)
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
func createAdmin(ctx context.Context, db *store.Store) error {
	login, password := os.Getenv("ADMIN_LOGIN"), os.Getenv("ADMIN_PASSWORD")
	if login == "" || utf8.RuneCountInString(login) > 32 || len(password) < 12 {
		return errors.New("ADMIN_LOGIN (1-32 characters) and ADMIN_PASSWORD (12-1024 bytes) are required")
	}
	id, err := strconv.ParseInt(env("ADMIN_ID", "1"), 10, 32)
	if err != nil || id <= 0 {
		return errors.New("invalid ADMIN_ID")
	}
	role, err := strconv.ParseInt(env("ADMIN_ROLE_ID", "4"), 10, 32)
	if err != nil || role <= 0 {
		return errors.New("invalid ADMIN_ROLE_ID")
	}
	room := env("ADMIN_ROOM", "000A")
	if utf8.RuneCountInString(room) > 8 {
		return errors.New("ADMIN_ROOM must be at most 8 characters")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	err = db.BootstrapAdmin(ctx, store.NewUser{ID: int32(id), Name: "Admin", Surname: "User", Room: room, Login: login, Password: hash, RoleID: int32(role)})
	if err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	slog.Info("administrator created", "id", id)
	return nil
}
func healthcheck() error {
	_, port, err := net.SplitHostPort(env("HTTP_ADDR", ":5002"))
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/readyz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("server is not ready")
	}
	return nil
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
