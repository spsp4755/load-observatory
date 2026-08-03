package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/spsp4755/load-observatory/internal/agent"
)

func main() {
	controllerURL := os.Getenv("CONTROLLER_URL")
	if controllerURL == "" { controllerURL = "http://127.0.0.1:8080" }
	for {
		ran, err := agent.RunOnce(context.Background(), controllerURL)
		if err != nil { log.Printf("agent: %v", err) }
		if !ran { time.Sleep(time.Second) }
	}
}
