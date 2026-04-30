#!/usr/bin/env bash
# Применяет новые миграции и обновляет сервис.
# Безопасно запускать многократно — миграции применяются только один раз.
set -euo pipefail

cd "$(dirname "$0")/.."

PG_EXEC="docker compose exec -T postgres psql -U todobot -d todobot"
MIGRATIONS_DIR="internal/db/migrations"

echo "📥 Подтягиваю изменения из git..."
git pull

echo ""
echo "🔧 Проверяю таблицу schema_migrations..."
$PG_EXEC -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename   TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
SQL

# Bootstrap: если БД уже работает (есть users), но schema_migrations пуст —
# помечаем все имеющиеся миграции как применённые. Это нужно один раз.
USERS_EXISTS=$($PG_EXEC -tA -c "SELECT to_regclass('public.users') IS NOT NULL" | tr -d '[:space:]')
APPLIED_COUNT=$($PG_EXEC -tA -c "SELECT count(*) FROM schema_migrations" | tr -d '[:space:]')

if [ "$USERS_EXISTS" = "t" ] && [ "$APPLIED_COUNT" = "0" ]; then
    echo "🌱 Первый запуск — помечаю существующие миграции как применённые:"
    for f in $(ls "$MIGRATIONS_DIR"/*.sql | sort); do
        fn=$(basename "$f")
        $PG_EXEC -c "INSERT INTO schema_migrations(filename) VALUES('$fn') ON CONFLICT DO NOTHING" >/dev/null
        echo "   ✓ $fn"
    done
fi

echo ""
echo "🚀 Применяю новые миграции..."
APPLIED_ANY=0
for f in $(ls "$MIGRATIONS_DIR"/*.sql | sort); do
    fn=$(basename "$f")
    is_applied=$($PG_EXEC -tA -c "SELECT 1 FROM schema_migrations WHERE filename='$fn'" | tr -d '[:space:]')
    if [ -z "$is_applied" ]; then
        echo "   → $fn"
        $PG_EXEC -v ON_ERROR_STOP=1 < "$f"
        $PG_EXEC -c "INSERT INTO schema_migrations(filename) VALUES('$fn')" >/dev/null
        APPLIED_ANY=1
    fi
done
if [ "$APPLIED_ANY" = "0" ]; then
    echo "   (нечего применять — всё актуально)"
fi

echo ""
echo "🏗️  Пересобираю и перезапускаю бота..."
docker compose build bot
docker compose up -d bot

echo ""
echo "📋 Последние логи:"
sleep 1
docker compose logs bot --tail=15

echo ""
echo "✅ Готово!"
