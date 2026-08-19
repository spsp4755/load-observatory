package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/spsp4755/load-observatory/internal/auth"
	"github.com/spsp4755/load-observatory/internal/controller"
	"github.com/spsp4755/load-observatory/internal/monitor"
	"github.com/spsp4755/load-observatory/internal/store"
)

func main() {
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = ":8080"
	}
	var data store.Store = store.NewMemoryStore()
	if url := os.Getenv("DATABASE_URL"); url != "" {
		key, err := store.DecodeEncryptionKey(os.Getenv("TARGET_API_KEY_ENCRYPTION_KEY"))
		if err != nil {
			log.Fatal(err)
		}
		postgres, err := store.NewPostgresStore(context.Background(), url, key)
		if err != nil {
			log.Fatal(err)
		}
		defer postgres.Close()
		data = postgres
	}
	server := controller.NewServerWithMonitor(data, monitor.New(os.Getenv("PROMETHEUS_URL"))).
		WithCaptureProxy(os.Getenv("CAPTURE_PROXY_TOKEN")).
		WithTargetAllowedHostSuffixes(os.Getenv("TARGET_ALLOWED_HOST_SUFFIXES"))
	if issuer := os.Getenv("OIDC_ISSUER_URL"); issuer != "" {
		sessionKey := []byte(os.Getenv("SESSION_SECRET"))
		if len(sessionKey) < 32 {
			log.Fatal("SESSION_SECRET must be set to at least 32 bytes when OIDC_ISSUER_URL is configured")
		}
		server = server.WithAuth(auth.NewGate(auth.Config{
			IssuerURL:    issuer,
			ClientID:     os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("OIDC_REDIRECT_URL"),
			SessionKey:   sessionKey,
		}))
		log.Printf("auth: Keycloak login required (issuer %s)", issuer)
	} else {
		log.Print("auth: OIDC_ISSUER_URL not set, running with no login (open access)")
	}
	log.Fatal(http.ListenAndServe(address, server))
}
