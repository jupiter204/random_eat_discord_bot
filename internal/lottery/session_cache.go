package lottery

import (
	"math/rand/v2"
	"sync"
	"time"

	"random_eat_discord/internal/googlemaps"
)

// QueryContext 儲存該次抽籤的搜尋條件與來源說明
type QueryContext struct {
	Keyword      string
	Latitude     float64
	Longitude    float64
	Radius       int
	LocationName string
	Initiator    string
}

// Session 代表一次抽籤的候選名單工作階段
type Session struct {
	ID        string
	Places    []googlemaps.Place
	PickedIDs map[string]bool
	QueryCtx  QueryContext
	ExpiresAt time.Time
}

// SessionCache 定義候選名單快取介面
type SessionCache interface {
	Store(sessionID string, places []googlemaps.Place, qCtx QueryContext, ttl time.Duration)
	PickNext(sessionID string) (*googlemaps.Place, int, *QueryContext, bool)
	Stop()
}

type inMemorySessionCache struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	stopChan chan struct{}
}

// NewSessionCache 建立 In-Memory 抽籤工作階段快取實例
func NewSessionCache() SessionCache {
	c := &inMemorySessionCache{
		sessions: make(map[string]*Session),
		stopChan: make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// Store 儲存批次候選清單
func (c *inMemorySessionCache) Store(sessionID string, places []googlemaps.Place, qCtx QueryContext, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessions[sessionID] = &Session{
		ID:        sessionID,
		Places:    places,
		PickedIDs: make(map[string]bool),
		QueryCtx:  qCtx,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// PickNext 隨機抽取一家尚未被抽過的餐廳，並回傳剩餘未抽數量
func (c *inMemorySessionCache) PickNext(sessionID string) (*googlemaps.Place, int, *QueryContext, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, exists := c.sessions[sessionID]
	if !exists || time.Now().After(s.ExpiresAt) {
		delete(c.sessions, sessionID)
		return nil, 0, nil, false
	}

	// 找出所有尚未抽過的餐廳索引
	var availableIndices []int
	for idx, p := range s.Places {
		if !s.PickedIDs[p.ID] {
			availableIndices = append(availableIndices, idx)
		}
	}

	// 若所有店家都抽過了一輪，則重設已抽紀錄（允許循環）
	if len(availableIndices) == 0 {
		s.PickedIDs = make(map[string]bool)
		for idx := range s.Places {
			availableIndices = append(availableIndices, idx)
		}
	}

	// 隨機選出一筆
	chosenIndex := availableIndices[rand.IntN(len(availableIndices))]
	chosenPlace := s.Places[chosenIndex]
	s.PickedIDs[chosenPlace.ID] = true

	remaining := len(s.Places) - len(s.PickedIDs)
	qCtx := s.QueryCtx

	return &chosenPlace, remaining, &qCtx, true
}

func (c *inMemorySessionCache) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for id, s := range c.sessions {
				if now.After(s.ExpiresAt) {
					delete(c.sessions, id)
				}
			}
			c.mu.Unlock()
		case <-c.stopChan:
			return
		}
	}
}

// Stop 停止背景清理 Goroutine
func (c *inMemorySessionCache) Stop() {
	close(c.stopChan)
}
