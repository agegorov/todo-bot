package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/aegorov/todo-bot/internal/db"
)

type Notifier interface {
	SendReminder(taskTitle string, deadline time.Time)
}

type Scheduler struct {
	cron    *cron.Cron
	queries *db.Queries
	bot     Notifier
}

func New(q *db.Queries, n Notifier) *Scheduler {
	c := cron.New(cron.WithSeconds())
	return &Scheduler{cron: c, queries: q, bot: n}
}

func (s *Scheduler) Start() {
	// Проверяем напоминания каждую минуту
	s.cron.AddFunc("0 * * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.fireReminders(ctx)
	})

	// Еженедельный дайджест — пятница 18:00
	s.cron.AddFunc("0 0 18 * * 5", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.weeklyDigest(ctx)
	})

	s.cron.Start()
	log.Println("scheduler: started")
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) fireReminders(ctx context.Context) {
	reminders, err := s.queries.ListDueReminders(ctx)
	if err != nil {
		log.Printf("scheduler: list reminders: %v", err)
		return
	}
	for _, r := range reminders {
		s.bot.SendReminder(r.TaskTitle, r.TaskDeadline.Time)
		if err := s.queries.MarkReminderSent(ctx, r.ID); err != nil {
			log.Printf("scheduler: mark sent %d: %v", r.ID, err)
		}
	}
}

func (s *Scheduler) weeklyDigest(ctx context.Context) {
	overdue, err := s.queries.ListOverdueTasks(ctx)
	if err != nil {
		return
	}
	if len(overdue) == 0 {
		s.bot.SendReminder("Дайджест: просроченных задач нет 🎉", time.Now())
		return
	}
	s.bot.SendReminder(
		"Еженедельный дайджест: "+string(rune('0'+len(overdue)))+" просроченных задач",
		time.Now(),
	)
}
