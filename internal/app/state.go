package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

type ActionType string

const (
	ActionNone ActionType = ""
	ActionBuy  ActionType = "buy"
	ActionSell ActionType = "sell"
)

type PersistentState struct {
	UpdatedAt time.Time               `json:"updated_at"`
	Symbols   map[string]*SymbolState `json:"symbols"`
}

type SymbolState struct {
	LastAction      ActionType         `json:"last_action"`
	LastBuyPrice    string             `json:"last_buy_price,omitempty"`
	LastSellPrice   string             `json:"last_sell_price,omitempty"`
	BuyLadder       []string           `json:"buy_ladder,omitempty"`
	SellAnchors     []string           `json:"sell_anchors,omitempty"`
	ConfigSignature string             `json:"config_signature,omitempty"`
	BuyArmed        bool               `json:"buy_armed,omitempty"`
	BuyLowest       string             `json:"buy_lowest,omitempty"`
	SellArmed       bool               `json:"sell_armed,omitempty"`
	SellHighest     string             `json:"sell_highest,omitempty"`
	LastActionAt    *time.Time         `json:"last_action_at,omitempty"`
	LastFilledAt    *time.Time         `json:"last_filled_at,omitempty"`
	LastDecision    string             `json:"last_decision,omitempty"`
	LastError       string             `json:"last_error,omitempty"`
	LastOrderStatus string             `json:"last_order_status,omitempty"`
	LastOrderMsg    string             `json:"last_order_msg,omitempty"`
	Pending         *PendingOrderState `json:"pending,omitempty"`
}

type PendingOrderState struct {
	OrderID        string     `json:"order_id"`
	Side           ActionType `json:"side"`
	SubmittedPrice string     `json:"submitted_price"`
	SubmittedQty   int64      `json:"submitted_qty"`
	SubmittedAt    time.Time  `json:"submitted_at"`
}

type StateStore struct {
	mu    sync.RWMutex
	path  string
	state PersistentState
	dirty bool
}

func NewStateStore(path string) (*StateStore, error) {
	store := &StateStore{
		path: path,
		state: PersistentState{
			Symbols: make(map[string]*SymbolState),
		},
	}

	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *StateStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取状态文件失败: %w", err)
	}
	var state PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("解析状态文件失败: %w", err)
	}
	if state.Symbols == nil {
		state.Symbols = make(map[string]*SymbolState)
	}
	s.state = state
	return nil
}

func (s *StateStore) Get(symbol string) SymbolState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.state.Symbols[symbol]
	if !ok || item == nil {
		return SymbolState{}
	}
	return *item
}

// Update 只修改内存中的状态并标记为脏,不立即写盘。
// 真正落盘集中在生命周期节点(优雅退出 / 崩溃恢复)由 Flush 完成,
// 以避免每个轮询周期内对全量状态文件做数十次序列化与替换。
func (s *StateStore) Update(symbol string, fn func(*SymbolState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.state.Symbols[symbol]
	if !ok || item == nil {
		item = &SymbolState{}
		s.state.Symbols[symbol] = item
	}
	fn(item)
	s.state.UpdatedAt = time.Now().UTC()
	s.dirty = true
	return nil
}

// Flush 在有未落盘修改时,将当前内存状态原子写入磁盘。无修改时为空操作。
func (s *StateStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *StateStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(s.path)), 0o755); err != nil && filepath.Dir(filepath.Clean(s.path)) != "." {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时状态文件失败: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("替换状态文件失败: %w", err)
	}
	return nil
}

func decimalStringPtr(value string) *decimal.Decimal {
	if value == "" {
		return nil
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func lastString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func popLast(values []string) []string {
	if len(values) == 0 {
		return values
	}
	return values[:len(values)-1]
}
