package main

import (
	"log"
	"net/http"
	"os"

	"github.com/spsp4755/load-observatory/internal/controller"
	"github.com/spsp4755/load-observatory/internal/store"
)

func main() {
	address := os.Getenv("LISTEN_ADDR")
	if address == "" { address = ":8080" }
	log.Fatal(http.ListenAndServe(address, controller.NewServer(store.NewMemoryStore())))
}
