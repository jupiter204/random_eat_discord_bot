package lottery

import (
	"sync"
	"testing"
	"time"

	"random_eat_discord/internal/googlemaps"
)

func TestSessionCache_TTL(t *testing.T) {
	cache := NewSessionCache()
	defer cache.Stop()

	places := []googlemaps.Place{
		{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "餐廳 1"}},
	}
	qCtx := QueryContext{Radius: 1000}

	sessionID := "ttl-session"
	// 50ms TTL
	cache.Store(sessionID, places, qCtx, 50*time.Millisecond)

	// 尚未過期
	sess, exists := cache.Get(sessionID)
	if !exists || sess == nil {
		t.Fatalf("expected session to exist before TTL expiration")
	}

	p, remaining, _, err := cache.PickNext(sessionID)
	if err != nil || p == nil || remaining != 0 {
		t.Fatalf("expected successful pick before TTL expiration, got err: %v", err)
	}

	// 等待過期
	time.Sleep(70 * time.Millisecond)

	_, exists = cache.Get(sessionID)
	if exists {
		t.Errorf("expected session to be expired and not found")
	}

	_, _, _, err = cache.PickNext(sessionID)
	if err != ErrSessionExpired {
		t.Errorf("expected ErrSessionExpired, got %v", err)
	}
}

func TestSessionCache_NoCycleExhaustion(t *testing.T) {
	cache := NewSessionCache()
	defer cache.Stop()

	places := []googlemaps.Place{
		{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "餐廳 A"}},
		{ID: "p2", DisplayName: googlemaps.LocalizedText{Text: "餐廳 B"}},
		{ID: "p3", DisplayName: googlemaps.LocalizedText{Text: "餐廳 C"}},
	}
	qCtx := QueryContext{Radius: 1000}
	sessionID := "exhaust-session"

	cache.Store(sessionID, places, qCtx, 5*time.Second)

	picked := make(map[string]bool)
	for i := 0; i < 3; i++ {
		p, remaining, _, err := cache.PickNext(sessionID)
		if err != nil {
			t.Fatalf("PickNext failed at step %d: %v", i, err)
		}
		if picked[p.ID] {
			t.Fatalf("Duplicate place picked: %s", p.ID)
		}
		picked[p.ID] = true
		expectedRemaining := 3 - (i + 1)
		if remaining != expectedRemaining {
			t.Errorf("expected remaining %d, got %d", expectedRemaining, remaining)
		}
	}

	// 第 4 次抽籤：所有候選已用罄，不得再循環重抽，必須回傳 ErrNoMoreCandidates
	p, _, _, err := cache.PickNext(sessionID)
	if err != ErrNoMoreCandidates {
		t.Fatalf("expected ErrNoMoreCandidates, got p=%v, err=%v", p, err)
	}
}

func TestSessionCache_StopIdempotent(t *testing.T) {
	cache := NewSessionCache()

	// 呼叫多次 Stop() 不得 panic
	cache.Stop()
	cache.Stop()
	cache.Stop()
}

func TestSessionCache_ConcurrentAccess(t *testing.T) {
	cache := NewSessionCache()
	defer cache.Stop()

	places := []googlemaps.Place{
		{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "餐廳 1"}},
		{ID: "p2", DisplayName: googlemaps.LocalizedText{Text: "餐廳 2"}},
		{ID: "p3", DisplayName: googlemaps.LocalizedText{Text: "餐廳 3"}},
		{ID: "p4", DisplayName: googlemaps.LocalizedText{Text: "餐廳 4"}},
		{ID: "p5", DisplayName: googlemaps.LocalizedText{Text: "餐廳 5"}},
	}
	qCtx := QueryContext{Radius: 1000}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sessionID := "concurrent-session"

			if idx%4 == 0 {
				cache.Store(sessionID, places, qCtx, 1*time.Minute)
			} else if idx%4 == 1 {
				_, _ = cache.Get(sessionID)
			} else if idx%4 == 2 {
				_, _, _, _ = cache.PickNext(sessionID)
			} else {
				cache.Delete(sessionID)
			}
		}(i)
	}

	wg.Wait()
}
