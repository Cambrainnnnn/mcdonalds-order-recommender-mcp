package ids

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Epoch is 2025-01-01 UTC. Layout: 41 timestamp, 10 worker, 12 sequence bits.
const Epoch int64 = 1735689600000

// SnowflakeGenerator is the process-local fallback for the in-memory catalog.
// PostgreSQL deployments use the durable database allocator instead.
type SnowflakeGenerator struct {
	mu       sync.Mutex
	worker   int64
	last     int64
	sequence int64
	now      func() time.Time
}

func NewSnowflakeGenerator(worker int64) (*SnowflakeGenerator, error) {
	if worker < 1 || worker > 1023 {
		return nil, fmt.Errorf("snowflake worker must be between 1 and 1023 (0 is reserved for PostgreSQL)")
	}
	return &SnowflakeGenerator{worker: worker, last: -1, now: time.Now}, nil
}

func (g *SnowflakeGenerator) NewID() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := g.now().UnixMilli() - Epoch
	if ms < 0 || ms >= 1<<41 {
		return "", fmt.Errorf("snowflake timestamp outside supported epoch")
	}
	if ms < g.last {
		return "", fmt.Errorf("snowflake clock moved backwards")
	}
	sequence := int64(0)
	if ms == g.last {
		sequence = g.sequence + 1
		if sequence > 4095 {
			// Bound the wait; a frozen clock must not hang requests indefinitely.
			time.Sleep(time.Millisecond)
			ms = g.now().UnixMilli() - Epoch
			if ms <= g.last || ms >= 1<<41 {
				return "", fmt.Errorf("snowflake sequence exhausted; retry after clock advances")
			}
			sequence = 0
		}
	}
	g.last, g.sequence = ms, sequence
	return strconv.FormatInt(ms<<22|g.worker<<12|sequence, 10), nil
}

// Default is shared by all in-memory services in this process.
var Default, _ = NewSnowflakeGenerator(1)
