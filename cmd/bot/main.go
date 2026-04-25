package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/aegorov/todo-bot/internal/api"
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

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	queries := db.New(pool)
	w := whisper.New(os.Getenv("WHISPER_ENDPOINT"))

	b, err := bot.New(os.Getenv("TELEGRAM_TOKEN"), ownerID, queries, w)
	if err != nil {
		log.Fatalf("bot init: %v", err)
	}

	sched := scheduler.New(queries, b)
	sched.Start()
	defer sched.Stop()

	// HTTP сервер для веб-интерфейса
	webPort := os.Getenv("WEB_PORT")
	if webPort == "" {
		webPort = "3000"
	}
	srv := &http.Server{
		Addr:    ":" + webPort,
		Handler: api.New(queries).Router(),
	}
	go func() {
		log.Printf("web: listening on :%s", webPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("web: %v", err)
		}
	}()
	defer srv.Shutdown(context.Background())

	log.Println("bot: started")
	b.Run(ctx)
	log.Println("bot: stopped")
}
