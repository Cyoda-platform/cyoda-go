package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Entity is the compute-test-client's local view of a cyoda entity.
// Decoupled from internal/common.Entity so this binary builds with
// no internal/ imports.
type Entity struct {
	ID    string          `json:"id"`
	State string          `json:"state"`
	Data  json.RawMessage `json:"data"`
}

// processorFunc is the signature of a registered processor.
type processorFunc func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error)

// criterionFunc is the signature of a registered criterion.
type criterionFunc func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error)

// catalog holds the named processors and criteria the compute test
// client serves over gRPC.
type catalog struct {
	processors map[string]processorFunc
	criteria   map[string]criterionFunc
}

// newCatalog returns a catalog populated with all registered entries.
func newCatalog() *catalog {
	return &catalog{
		processors: map[string]processorFunc{
			"noop": func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error) {
				return entity, nil
			},
			"tag-with-foo": func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error) {
				var data map[string]any
				if err := json.Unmarshal(entity.Data, &data); err != nil {
					return nil, err
				}
				data["tag"] = "foo"
				out, err := json.Marshal(data)
				if err != nil {
					return nil, err
				}
				return &Entity{ID: entity.ID, State: entity.State, Data: out}, nil
			},
			"bump-amount": func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error) {
				var data map[string]any
				if err := json.Unmarshal(entity.Data, &data); err != nil {
					return nil, err
				}
				if a, ok := data["amount"].(float64); ok {
					data["amount"] = a + 1
				}
				out, _ := json.Marshal(data)
				return &Entity{ID: entity.ID, State: entity.State, Data: out}, nil
			},
			"inject-error": func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error) {
				return nil, fmt.Errorf("inject-error: deliberate failure")
			},
			"slow-configurable": func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error) {
				var cfg struct{ SleepMS int `json:"sleep_ms"` }
				_ = json.Unmarshal(config, &cfg)
				if cfg.SleepMS > 0 {
					select {
					case <-time.After(time.Duration(cfg.SleepMS) * time.Millisecond):
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return entity, nil
			},
			"set-field": func(ctx context.Context, entity *Entity, config json.RawMessage) (*Entity, error) {
				var cfg struct {
					Field string `json:"field"`
					Value any    `json:"value"`
				}
				if err := json.Unmarshal(config, &cfg); err != nil {
					return nil, fmt.Errorf("set-field config: %w", err)
				}
				var data map[string]any
				if err := json.Unmarshal(entity.Data, &data); err != nil {
					return nil, err
				}
				data[cfg.Field] = cfg.Value
				out, _ := json.Marshal(data)
				return &Entity{ID: entity.ID, State: entity.State, Data: out}, nil
			},
		},
		criteria: map[string]criterionFunc{
			"always-true": func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error) {
				return true, nil
			},
			"always-false": func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error) {
				return false, nil
			},
			"amount-gt-100": func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error) {
				var data map[string]any
				if err := json.Unmarshal(entity.Data, &data); err != nil {
					return false, err
				}
				if a, ok := data["amount"].(float64); ok {
					return a > 100, nil
				}
				return false, nil
			},
			"select-premium": func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error) {
				var data map[string]any
				if err := json.Unmarshal(entity.Data, &data); err != nil {
					return false, err
				}
				if a, ok := data["amount"].(float64); ok {
					return a > 1000, nil
				}
				return false, nil
			},
			"select-standard": func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error) {
				return true, nil
			},
			"field-equals": func(ctx context.Context, entity *Entity, config json.RawMessage) (bool, error) {
				var cfg struct {
					Field string `json:"field"`
					Value any    `json:"value"`
				}
				if err := json.Unmarshal(config, &cfg); err != nil {
					return false, fmt.Errorf("field-equals config: %w", err)
				}
				var data map[string]any
				if err := json.Unmarshal(entity.Data, &data); err != nil {
					return false, err
				}
				return data[cfg.Field] == cfg.Value, nil
			},
		},
	}
}

func (c *catalog) processor(name string) (processorFunc, bool) {
	fn, ok := c.processors[name]
	return fn, ok
}

func (c *catalog) criterion(name string) (criterionFunc, bool) {
	fn, ok := c.criteria[name]
	return fn, ok
}
