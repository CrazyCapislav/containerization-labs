// Package store — работа с заказами в postgres.
// Общий код для api и worker: оба ходят в одну таблицу, но с разных сторон.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Order struct {
	ID        int64     `json:"id"`
	Item      string    `json:"item"`
	Qty       int       `json:"qty"`
	Processed bool      `json:"processed"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	db *sql.DB
}

// DSN собирается из переменных окружения. CloudNativePG кладёт в секрет
// приложения готовую строку подключения под ключом uri, её и используем;
// остальные переменные нужны, когда база поднимается иначе.
func dsn() string {
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u
	}
	host := envOr("PGHOST", "localhost")
	port := envOr("PGPORT", "5432")
	user := envOr("PGUSER", "postgres")
	pass := envOr("PGPASSWORD", "postgres")
	name := envOr("PGDATABASE", "shop")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, name)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// New открывает пул соединений. Важно: ошибка подключения здесь не фатальна.
// Сервис должен подниматься и без базы, иначе под не пройдёт readiness и
// выкатка повиснет из-за недоступности внешней зависимости.
func New() *Store {
	db, err := sql.Open("pgx", dsn())
	if err != nil {
		return &Store{db: nil}
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("пул соединений не создан")
	}
	return s.db.PingContext(ctx)
}

// Migrate создаёт таблицу, если её ещё нет. Для лабы этого достаточно,
// в настоящем проекте миграции выносят в отдельный шаг выкатки.
func (s *Store) Migrate(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("нет подключения к базе")
	}
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS orders (
			id         BIGSERIAL PRIMARY KEY,
			item       TEXT        NOT NULL,
			qty        INT         NOT NULL DEFAULT 1,
			processed  BOOLEAN     NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

func (s *Store) CreateOrder(ctx context.Context, item string, qty int) (*Order, error) {
	if s.db == nil {
		return nil, fmt.Errorf("нет подключения к базе")
	}
	o := &Order{Item: item, Qty: qty}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO orders (item, qty) VALUES ($1, $2)
		 RETURNING id, processed, created_at`,
		item, qty).Scan(&o.ID, &o.Processed, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) ListOrders(ctx context.Context, limit int) ([]Order, error) {
	if s.db == nil {
		return nil, fmt.Errorf("нет подключения к базе")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, item, qty, processed, created_at
		 FROM orders ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Order{}
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.Item, &o.Qty, &o.Processed, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ProcessBatch помечает обработанными до limit необработанных заказов.
// FOR UPDATE SKIP LOCKED позволяет нескольким worker'ам разбирать очередь
// одновременно, не блокируя друг друга и не обрабатывая один заказ дважды.
func (s *Store) ProcessBatch(ctx context.Context, limit int) ([]int64, error) {
	if s.db == nil {
		return nil, fmt.Errorf("нет подключения к базе")
	}
	rows, err := s.db.QueryContext(ctx, `
		UPDATE orders SET processed = TRUE
		WHERE id IN (
			SELECT id FROM orders
			WHERE processed = FALSE
			ORDER BY id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) PendingCount(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("нет подключения к базе")
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM orders WHERE processed = FALSE`).Scan(&n)
	return n, err
}
