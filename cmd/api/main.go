package main

import (
	"context"
	"log"

	"combox-backend/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		log.Fatalf("combox-backend failed: %v", err)
	}
}
