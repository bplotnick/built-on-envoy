// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package config loads configuration independently of Envoy, validating raw input
// before typed decoding so malformed values cannot be silently coerced.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Loader applies one schema and all semantic checks to filter and per-route input.
type Loader struct {
	schemaJSON []byte
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
}

// NewLoader owns a copy of the schema and compiles it on the first load.
func NewLoader(schemaJSON []byte) *Loader {
	return &Loader{schemaJSON: bytes.Clone(schemaJSON)}
}

// Load returns typed configuration only after raw schema and semantic validation.
func (l *Loader) Load(data []byte, source Source) (*FilterConfig, error) {
	if source != FilterSource && source != PerRouteSource {
		return nil, fmt.Errorf("invalid configuration source: %d", source)
	}
	if err := l.validateAgainstSchema(data, source); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}
	cfg, err := parseConfig(data, source)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return cfg, nil
}

func (l *Loader) validateAgainstSchema(data []byte, source Source) error {
	instance, err := decodeConfigInstance(data)
	if err != nil {
		return fmt.Errorf("failed to decode config for schema validation: %w", err)
	}
	schema, err := l.getSchema()
	if err != nil {
		return fmt.Errorf("failed to compile embedded config schema: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return err
	}
	if source == PerRouteSource {
		if root, ok := instance.(map[string]any); ok {
			if _, exists := root["endpoints"]; exists {
				return fmt.Errorf("endpoints cannot be used in per-route configuration; configure matching in Envoy routes")
			}
		}
	}
	return nil
}

func (l *Loader) getSchema() (*jsonschema.Schema, error) {
	l.schemaOnce.Do(func() {
		resource, err := jsonschema.UnmarshalJSON(bytes.NewReader(l.schemaJSON))
		if err != nil {
			l.schemaErr = err
			return
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("config.schema.json", resource); err != nil {
			l.schemaErr = err
			return
		}
		l.schema, l.schemaErr = compiler.Compile("config.schema.json")
	})
	return l.schema, l.schemaErr
}

func decodeConfigInstance(data []byte) (any, error) {
	trimmed := bytes.TrimSpace(data)
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var instance any
	if err := decoder.Decode(&instance); err != nil {
		return nil, err
	}
	var next any
	if err := decoder.Decode(&next); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("configuration must contain exactly one YAML document")
	}
	if json.Valid(trimmed) {
		decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(trimmed))
		if err != nil {
			return nil, err
		}
		instance = decoded
	}
	return instance, nil
}
