package store

import (
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

type poolEntry struct {
	db        *sqlx.DB
	throttle  *Throttle
	createdAt time.Time
	wg        sync.WaitGroup // tracks in-flight operation count
}
