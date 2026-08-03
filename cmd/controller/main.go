package main

import (
	"context"
	"log"
	"net/http"
	"os"

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
		postgres, err := store.NewPostgresStore(context.Background(), url)
		if err != nil {
			log.Fatal(err)
		}
		defer postgres.Close()
		data = postgres
	}
	log.Fatal(http.ListenAndServe(address, controller.NewServerWithMonitor(data, monitor.New(os.Getenv("PROMETHEUS_URL")))))
}
