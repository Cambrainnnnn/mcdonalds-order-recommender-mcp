package ids

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSnowflakeLayoutAndClock(t *testing.T) {
	g, _ := NewSnowflakeGenerator(42)
	ms := Epoch + 123456
	g.now = func() time.Time { return time.UnixMilli(ms) }
	for i := int64(0); i < 4096; i++ {
		value, err := g.NewID()
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n>>22 != 123456 || (n>>12)&1023 != 42 || n&4095 != i {
			t.Fatalf("incorrect layout: %s", value)
		}
	}
	if _, err := g.NewID(); err == nil {
		t.Fatal("frozen clock overflow accepted")
	}
	ms--
	if _, err := g.NewID(); err == nil {
		t.Fatal("clock rollback accepted")
	}
	ms += 2
	if _, err := g.NewID(); err != nil {
		t.Fatal(err)
	}
	for _, worker := range []int64{-1, 0, 1024} {
		if _, err := NewSnowflakeGenerator(worker); err == nil {
			t.Fatalf("accepted worker %d", worker)
		}
	}
	ms = Epoch + 1<<41
	if _, err := g.NewID(); err == nil {
		t.Fatal("timestamp overflow accepted")
	}
}

func TestSnowflakeConcurrentUniqueness(t *testing.T) {
	g, _ := NewSnowflakeGenerator(1)
	values := make(chan string, 10000)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				id, err := g.NewID()
				if err != nil {
					t.Error(err)
					return
				}
				values <- id
			}
		}()
	}
	wg.Wait()
	close(values)
	seen := map[string]bool{}
	for id := range values {
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 10000 {
		t.Fatalf("got %d IDs", len(seen))
	}
}
