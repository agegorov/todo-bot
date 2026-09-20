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
	"github.com/aegorov/todo-bot/internal/recurrence"
	"github.com/aegorov/todo-bot/internal/whisper"
)

type Bot struct {
	api     *tgbotapi.BotAPI
	queries *db.Queries
	whisper *whisper.Client
}

func New(token string, q *db.Queries, w *whisper.Client) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	return &Bot{api: api, queries: q, whisper: w}, nil
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
			// Бот мультипользовательский: сообщения принимаются от любого
			// Telegram-юзера. Изоляция данных — на уровне БД (user_id /
			// telegram_user_id), а не входным гейтом по единственному owner ID.
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
	_, err := b.createTaskFromText(ctx, msg.Text, msg.Chat.ID, msg.From.ID)
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
	b.createTaskFromText(ctx, text, msg.Chat.ID, msg.From.ID)
}

func (b *Bot) createTaskFromText(ctx context.Context, text string, chatID int64, telegramUserID int64) (*db.Task, error) {
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

	// Если Telegram-аккаунт уже привязан — сразу сохраняем user_id, иначе задача
	// зависнет как orphan и не появится ни на чьей доске.
	linkedUserID := b.resolveLinkedUserID(ctx, telegramUserID)

	task, err := b.queries.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:      projectID,
		Title:          parsed.Title,
		Notes:          notes,
		Priority:       int16(parsed.Priority),
		Deadline:       deadline,
		DelegatedTo:    delegated,
		IsRecurring:    parsed.IsRecurring,
		RecurRule:      recurRule,
		UserID:         linkedUserID,
		TelegramUserID: &telegramUserID,
	})
	if err != nil {
		return nil, fmt.Errorf("сохранение задачи: %w", err)
	}

	// И сразу кладём в системную TO DO колонку — без неё карточка не появится
	// на доске, даже если user_id проставлен.
	if linkedUserID != nil {
		if todoCol, err := b.queries.EnsureTodoColumn(ctx, linkedUserID); err == nil {
			_ = b.queries.MoveTaskToColumn(ctx, db.MoveTaskToColumnParams{
				ID: task.ID, ColumnID: todoCol.ID, UserID: linkedUserID,
			})
		}
	}

	// Все задачи созданные через Telegram автоматически помечаются #telegram
	tags := append(parsed.Tags, "telegram")
	seen := make(map[string]bool, len(tags))
	for _, tagName := range tags {
		key := strings.ToLower(tagName)
		if seen[key] {
			continue
		}
		seen[key] = true
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
		b.cmdList(ctx, msg.Chat.ID, msg.From.ID)
	case "/today":
		b.cmdToday(ctx, msg.Chat.ID, msg.From.ID)
	case "/overdue":
		b.cmdOverdue(ctx, msg.Chat.ID, msg.From.ID)
	case "/done":
		if len(parts) < 2 {
			b.send(msg.Chat.ID, "Использование: /done <id>")
			return
		}
		b.cmdDone(ctx, msg.Chat.ID, msg.From.ID, parts[1])
	case "/projects":
		b.cmdProjects(ctx, msg.Chat.ID)
	case "/link":
		if len(parts) < 2 {
			b.send(msg.Chat.ID, "Использование: /link <токен>\nТокен можно получить на сайте в настройках.")
			return
		}
		b.cmdLink(ctx, msg.Chat.ID, msg.From.ID, parts[1])
	case "/start", "/help":
		b.send(msg.Chat.ID, helpText())
	default:
		b.send(msg.Chat.ID, "Неизвестная команда. /help — список команд.")
	}
}

// resolveLinkedUserID возвращает ID веб-аккаунта, привязанного к этому Telegram-юзеру,
// или nil, если привязки ещё нет (orphan-состояние до /link).
func (b *Bot) resolveLinkedUserID(ctx context.Context, telegramUserID int64) *int64 {
	u, err := b.queries.GetUserByTelegramID(ctx, &telegramUserID)
	if err != nil {
		return nil
	}
	id := u.ID
	return &id
}

func (b *Bot) cmdList(ctx context.Context, chatID, telegramUserID int64) {
	tasks, err := b.queries.ListOpenTasksForTelegram(ctx, db.ListOpenTasksForTelegramParams{
		UserID: b.resolveLinkedUserID(ctx, telegramUserID), TelegramUserID: telegramUserID,
	})
	if err != nil || len(tasks) == 0 {
		b.send(chatID, "Задач нет 🎉")
		return
	}
	b.send(chatID, formatOpenTasks("Все открытые задачи", tasks))
}

func (b *Bot) cmdToday(ctx context.Context, chatID, telegramUserID int64) {
	tasks, err := b.queries.ListTodayTasksForTelegram(ctx, db.ListTodayTasksForTelegramParams{
		UserID: b.resolveLinkedUserID(ctx, telegramUserID), TelegramUserID: telegramUserID,
	})
	if err != nil || len(tasks) == 0 {
		b.send(chatID, "На сегодня задач нет 🎉")
		return
	}
	b.send(chatID, formatTodayTasks("Задачи на сегодня", tasks))
}

func (b *Bot) cmdOverdue(ctx context.Context, chatID, telegramUserID int64) {
	tasks, err := b.queries.ListOverdueTasksForTelegram(ctx, db.ListOverdueTasksForTelegramParams{
		UserID: b.resolveLinkedUserID(ctx, telegramUserID), TelegramUserID: telegramUserID,
	})
	if err != nil || len(tasks) == 0 {
		b.send(chatID, "Просроченных задач нет ✅")
		return
	}
	b.send(chatID, formatOverdueTasks("⚠️ Просроченные", tasks))
}

func (b *Bot) cmdDone(ctx context.Context, chatID int64, telegramUserID int64, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		b.send(chatID, "Неверный ID задачи.")
		return
	}

	// Если пользователь привязан — переносим в его Done колонку
	user, uerr := b.queries.GetUserByTelegramID(ctx, &telegramUserID)
	if uerr == nil {
		_, _ = b.queries.EnsureDoneColumn(ctx, &user.ID)

		// Читаем задачу до закрытия — для возможного спавна следующей итерации
		task, _ := b.queries.GetTaskForUser(ctx, db.GetTaskForUserParams{
			ID: id, UserID: &user.ID,
		})

		if err := b.queries.CompleteTaskForUser(ctx, db.CompleteTaskForUserParams{
			ID: id, UserID: &user.ID,
		}); err != nil {
			b.send(chatID, "❌ Ошибка: "+err.Error())
			return
		}

		// Если задача периодическая — создаём следующую копию
		if nextID, _ := recurrence.Spawn(ctx, b.queries, task, user.ID); nextID > 0 {
			b.send(chatID, fmt.Sprintf("✅ Задача #%d закрыта!\n🔁 Создана следующая итерация #%d", id, nextID))
			return
		}
	} else {
		// Не привязан — закрываем только собственную orphan-задачу этого TG-юзера.
		// Без этой проверки (после снятия owner-гейта) любой смог бы закрыть чужую
		// задачу, просто угадав/подобрав id.
		affected, err := b.queries.CompleteOrphanTaskForTelegram(ctx, db.CompleteOrphanTaskForTelegramParams{
			ID: id, TelegramUserID: telegramUserID,
		})
		if err != nil {
			b.send(chatID, "❌ Ошибка: "+err.Error())
			return
		}
		if affected == 0 {
			b.send(chatID, "Задача не найдена — возможно, она не твоя или уже закрыта.")
			return
		}
	}
	b.send(chatID, fmt.Sprintf("✅ Задача #%d закрыта!", id))
}

func (b *Bot) cmdLink(ctx context.Context, chatID int64, telegramUserID int64, token string) {
	row, err := b.queries.GetLinkToken(ctx, token)
	if err != nil {
		b.send(chatID, "❌ Токен недействителен или истёк. Получи новый на сайте.")
		return
	}

	// Привязываем telegram_id к веб-аккаунту
	if err := b.queries.SetUserTelegramID(ctx, db.SetUserTelegramIDParams{
		ID:         row.UserID,
		TelegramID: &telegramUserID,
	}); err != nil {
		b.send(chatID, "❌ Ошибка привязки: "+err.Error())
		return
	}

	// Переносим все orphan задачи этого Telegram пользователя
	_ = b.queries.ClaimTasksByTelegram(ctx, db.ClaimTasksByTelegramParams{
		UserID:         &row.UserID,
		TelegramUserID: &telegramUserID,
	})

	// Гарантируем обе системные колонки
	_, _ = b.queries.EnsureDoneColumn(ctx, &row.UserID)
	if todoCol, err := b.queries.EnsureTodoColumn(ctx, &row.UserID); err == nil {
		_ = b.queries.MoveOrphanTasksToColumn(ctx, db.MoveOrphanTasksToColumnParams{
			ColumnID: todoCol.ID,
			UserID:   &row.UserID,
		})
	}

	// Удаляем использованный токен
	_ = b.queries.DeleteLinkToken(ctx, token)

	b.send(chatID, fmt.Sprintf(
		"✅ Аккаунт привязан!\n\nВеб: *%s*\nВсе твои задачи теперь доступны на доске.",
		row.Email,
	))
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

// SendReminder отправляет напоминание конкретному чату — вызывается планировщиком.
// chatID приходит из ListDueReminders: telegram_id привязанного веб-юзера, либо
// telegram_user_id самой задачи, если аккаунт ещё не привязан.
func (b *Bot) SendReminder(chatID int64, taskTitle string, deadline time.Time) {
	text := fmt.Sprintf("⏰ Напоминание: *%s*\nДедлайн: %s", taskTitle, deadline.Format("02 Jan 15:04"))
	b.send(chatID, text)
}

// SendMessage отправляет произвольный текст конкретному чату (например, дайджест).
func (b *Bot) SendMessage(chatID int64, text string) {
	b.send(chatID, text)
}
