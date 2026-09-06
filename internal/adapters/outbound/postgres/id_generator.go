package postgres

import (
	"context"
	"database/sql"
	"time"
)

// IDGenerator uses a durable, serialized worker (0), shared across API replicas.
// Allocation commits before the ID is returned, including across restarts.
type IDGenerator struct{ db *sql.DB }

func NewIDGenerator(db *sql.DB) *IDGenerator { return &IDGenerator{db: db} }

func (g *IDGenerator) NewID() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id string
	err := g.db.QueryRowContext(ctx, `SELECT next_snowflake_id()`).Scan(&id)
	return id, err
}
