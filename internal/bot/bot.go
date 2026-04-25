package bot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/aegorov/todo-bot/internal/db"
	"github.com/aegorov/todo-bot/internal/parser"
	"github.com/aegorov/todo-bot/internal/whisper"
)

type Bot struct {
	api     *tgbotapi.BotAPI
	queries *db.Queries
	whisper *whisper.Client
	ownerID int64
}

func New(token string, ownerID int64, q *db.Queries, w *whisper.Client) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	return &Bot{api: api, queries: q, whisper: w, ownerID: ownerID}, nil
}

func (b *Bot) Run(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.Message == nil {
				continue
			}
			if update.Message.From.ID != b.ownerID {
				b.send(update.Message.Chat.ID, "Доступ запрещён.")
				continue
			}
			go b.handleMessage(ctx, update.Message)
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	switch {
	case msg.Voice != nil:
		b.handleVoice(ctx, msg)
	case msg.Text != "" && strings.HasPrefix(msg.Text, "/"):
		b.handleCommand(ctx, msg)
	case msg.Text != "":
		b.handleText(ctx, msg)
	}
}

func (b *Bot) handleText(ctx context.Context, msg *tgbotapi.Message) {
	_, err := b.createTaskFromText(ctx, msg.Text, msg.Chat.ID)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+err.Error())
	}
}

func (b *Bot) handleVoice(ctx context.Context, msg *tgbotapi.Message) {
	b.send(msg.Chat.ID, "🎤 Транскрибирую...")

	audioPath, err := b.downloadVoice(msg.Voice.FileID)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Не удалось скачать аудио: "+err.Error())
		return
	}
	defer os.Remove(audioPath)

	text, err := b.whisper.Transcribe(ctx, audioPath)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка транскрипции: "+err.Error())
		return
	}

	b.send(msg.Chat.ID, "📝 Распознано: "+text)
	b.createTaskFromText(ctx, text, msg.Chat.ID)
}

func (b *Bot) createTaskFromText(ctx context.Context, text string, chatID int64) (*db.Task, error) {
	parsed := parser.Parse(text, time.Now())

	projectID, err := b.resolveProject(ctx, parsed.Project)
	if err != nil {
		projectID = 1 // Inbox fallback
	}

	var deadline pgtype.Timestamptz
	if parsed.HasDeadline {
		deadline = pgtype.Timestamptz{Time: parsed.Deadline, Valid: true}
	}

	var notes *string
	if parsed.Notes != "" {
		notes = &parsed.Notes
	}
	var delegated *string
	if parsed.DelegatedTo != "" {
		delegated = &parsed.DelegatedTo
	}
	var recurRule *string
	if parsed.RecurRule != "" {
		recurRule = &parsed.RecurRule
	}

	task, err := b.queries.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:   projectID,
		Title:       parsed.Title,
		Notes:       notes,
		Priority:    int16(parsed.Priority),
		Deadline:    deadline,
		DelegatedTo: delegated,
		IsRecurring: parsed.IsRecurring,
		RecurRule:   recurRule,
	})
	if err != nil {
		return nil, fmt.Errorf("сохранение задачи: %w", err)
	}

	for _, tagName := range parsed.Tags {
		tag, err := b.queries.UpsertTag(ctx, tagName)
		if err != nil {
			continue
		}
		_ = b.queries.AttachTag(ctx, db.AttachTagParams{TaskID: task.ID, TagID: tag.ID})
	}

	if parsed.HasDeadline {
		remind := parsed.Deadline.Add(-time.Hour)
		if remind.After(time.Now()) {
			_, _ = b.queries.CreateReminder(ctx, db.CreateReminderParams{
				TaskID:   task.ID,
				RemindAt: pgtype.Timestamptz{Time: remind, Valid: true},
			})
		}
	}

	b.send(chatID, formatTaskCreated(&task))
	return &task, nil
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	parts := strings.Fields(msg.Text)
	cmd := parts[0]

	switch cmd {
	case "/list":
		b.cmdList(ctx, msg.Chat.ID)
	case "/today":
		b.cmdToday(ctx, msg.Chat.ID)
	case "/overdue":
		b.cmdOverdue(ctx, msg.Chat.ID)
	case "/done":
		if len(parts) < 2 {
			b.send(msg.Chat.ID, "Использование: /done <id>")
			return
		}
		b.cmdDone(ctx, msg.Chat.ID, parts[1])
	case "/projects":
		b.cmdProjects(ctx, msg.Chat.ID)
	case "/start", "/help":
		b.send(msg.Chat.ID, helpText())
	default:
		b.send(msg.Chat.ID, "Неизвестная команда. /help — список команд.")
	}
}

func (b *Bot) cmdList(ctx context.Context, chatID int64) {
	tasks, err := b.queries.ListOpenTasks(ctx)
	if err != nil || len(tasks) == 0 {
		b.send(chatID, "Задач нет 🎉")
		return
	}
	b.send(chatID, formatOpenTasks("Все открытые задачи", tasks))
}

func (b *Bot) cmdToday(ctx context.Context, chatID int64) {
	tasks, err := b.queries.ListTodayTasks(ctx)
	if err != nil || len(tasks) == 0 {
		b.send(chatID, "На сегодня задач нет 🎉")
		return
	}
	b.send(chatID, formatTodayTasks("Задачи на сегодня", tasks))
}

func (b *Bot) cmdOverdue(ctx context.Context, chatID int64) {
	tasks, err := b.queries.ListOverdueTasks(ctx)
	if err != nil || len(tasks) == 0 {
		b.send(chatID, "Просроченных задач нет ✅")
		return
	}
	b.send(chatID, formatOverdueTasks("⚠️ Просроченные", tasks))
}

func (b *Bot) cmdDone(ctx context.Context, chatID int64, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		b.send(chatID, "Неверный ID задачи.")
		return
	}
	if err := b.queries.CompleteTask(ctx, id); err != nil {
		b.send(chatID, "❌ Ошибка: "+err.Error())
		return
	}
	b.send(chatID, fmt.Sprintf("✅ Задача #%d закрыта!", id))
}

func (b *Bot) cmdProjects(ctx context.Context, chatID int64) {
	projects, err := b.queries.ListProjects(ctx)
	if err != nil {
		b.send(chatID, "Ошибка загрузки проектов.")
		return
	}
	var sb strings.Builder
	sb.WriteString("📁 Проекты:\n")
	for _, p := range projects {
		sb.WriteString(fmt.Sprintf("  %s %s\n", p.Color, p.Name))
	}
	b.send(chatID, sb.String())
}

func (b *Bot) resolveProject(ctx context.Context, name string) (int64, error) {
	projects, err := b.queries.ListProjects(ctx)
	if err != nil {
		return 1, err
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, name) {
			return p.ID, nil
		}
	}
	return 1, nil
}

func (b *Bot) downloadVoice(fileID string) (string, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", err
	}
	url := file.Link(b.api.Token)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp("", "voice-*.ogg")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", err
	}
	return filepath.Abs(tmp.Name())
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send error: %v", err)
	}
}

// SendReminder отправляет напоминание — вызывается планировщиком.
func (b *Bot) SendReminder(taskTitle string, deadline time.Time) {
	text := fmt.Sprintf("⏰ Напоминание: *%s*\nДедлайн: %s", taskTitle, deadline.Format("02 Jan 15:04"))
	b.send(b.ownerID, text)
}
