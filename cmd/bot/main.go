package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"

	"github.com/aegorov/todo-bot/internal/bot"
	"github.com/aegorov/todo-bot/internal/db"
	"github.com/aegorov/todo-bot/internal/scheduler"
	"github.com/aegorov/todo-bot/internal/whisper"
)

func main() {
	_ = godotenv.Load()

	required := []string{"TELEGRAM_TOKEN", "TELEGRAM_OWNER_ID", "DATABASE_URL", "WHISPER_ENDPOINT"}
	for _, key := range required {
		if os.Getenv(key) == "" {
			log.Fatalf("missing env: %s", key)
		}
	}

	ownerID, err := strconv.ParseInt(os.Getenv("TELEGRAM_OWNER_ID"), 10, 64)
	if err != nil {
		log.Fatalf("invalid TELEGRAM_OWNER_ID: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer conn.Close(ctx)

	queries := db.New(conn)
	w := whisper.New(os.Getenv("WHISPER_ENDPOINT"))

	b, err := bot.New(os.Getenv("TELEGRAM_TOKEN"), ownerID, queries, w)
	if err != nil {
		log.Fatalf("bot init: %v", err)
	}

	sched := scheduler.New(queries, b)
	sched.Start()
	defer sched.Stop()

	log.Println("bot: started")
	b.Run(ctx)
	log.Println("bot: stopped")
}
