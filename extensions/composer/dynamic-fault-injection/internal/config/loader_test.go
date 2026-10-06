// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Loader tests exercise the complete public loading boundary because validating
// already decoded values would miss coercion and document-shape errors.

package config

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

var testLoader *Loader

func TestMain(m *testing.M) {
	schema, err := os.ReadFile("../../config.schema.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read canonical configuration schema: %v\n", err)
		os.Exit(1)
	}
	testLoader = NewLoader(schema)
	os.Exit(m.Run())
}

func loadFilterConfig(data []byte) (*FilterConfig, error) {
	return testLoader.Load(data, FilterSource)
}

func loadPerRouteConfig(data []byte) (*FilterConfig, error) {
	return testLoader.Load(data, PerRouteSource)
}

func TestLoader_ValidatesRawValuesBeforeTypedDecoding(t *testing.T) {
	for name, data := range map[string]string{
		"numeric status string": `responses: [{status: "200", resolution: 1, distribution: {p0.0: 1ms}}]`,
		"numeric body":          `responses: [{status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: {body: 123}}]`,
		"null body":             `responses: [{status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: {body: null}}]`,
		"null local response":   `responses: [{status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: null}]`,
		"numeric header":        `responses: [{status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: {headers: [{name: x-extra, value: 123}]}}]`,
		"missing header name":   `responses: [{status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: {headers: [{value: ok}]}}]`,
		"null headers":          `responses: [{status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: {headers: null}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			for _, source := range []Source{FilterSource, PerRouteSource} {
				cfg, err := testLoader.Load([]byte(data), source)
				require.Nil(t, cfg)
				var schemaErr *jsonschema.ValidationError
				require.ErrorAs(t, err, &schemaErr)
			}
		})
	}
}

func TestLoader_PerRouteRejectsMergedEndpointSelectors(t *testing.T) {
	data := []byte("<<: &defaults {endpoints: []}\nresponses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}}]\n")
	cfg, err := testLoader.Load(data, PerRouteSource)
	require.Nil(t, cfg)
	require.ErrorContains(t, err, "endpoints cannot be used in per-route configuration")
	_, err = testLoader.Load(data, FilterSource)
	require.NoError(t, err)
}

func TestLoader_RejectsInvalidDocuments(t *testing.T) {
	for name, data := range map[string]string{
		"empty":           "",
		"null":            "null",
		"empty object":    "{}",
		"second document": "endpoints: []\n---\nnull\n",
		"duplicate key":   "endpoints: []\nendpoints: []\n",
		"recursive alias": "responses: &loop [*loop]\n",
	} {
		t.Run(name, func(t *testing.T) {
			for _, source := range []Source{FilterSource, PerRouteSource} {
				cfg, err := testLoader.Load([]byte(data), source)
				require.Nil(t, cfg)
				require.Error(t, err)
			}
		})
	}
	cfg, err := loadFilterConfig([]byte("endpoints: []"))
	require.NoError(t, err)
	require.Empty(t, cfg.Endpoints)
	cfg, err = loadPerRouteConfig([]byte("endpoints: []"))
	require.Nil(t, cfg)
	require.Error(t, err)
	cfg, err = testLoader.Load([]byte("endpoints: []"), Source(-1))
	require.Nil(t, cfg)
	require.ErrorContains(t, err, "invalid configuration source")
}

func TestLoader_CompiledSchemasAreIsolatedAndOwned(t *testing.T) {
	data := []byte("endpoints: []")
	invalid := NewLoader([]byte("{"))
	_, firstErr := invalid.Load(data, FilterSource)
	require.ErrorContains(t, firstErr, "failed to compile embedded config schema")
	for range 2 {
		cfg, err := invalid.Load(data, FilterSource)
		require.Nil(t, cfg)
		require.EqualError(t, err, firstErr.Error())
	}

	schema := []byte(`{"type":"object"}`)
	valid := NewLoader(schema)
	clear(schema)
	cfg, err := valid.Load(data, FilterSource)
	require.NoError(t, err)
	require.Empty(t, cfg.Endpoints)
}

func TestLoader_KeepsStrictDecodingAfterSchemaValidation(t *testing.T) {
	loader := NewLoader([]byte(`{"type":"object"}`))
	cfg, err := loader.Load([]byte("endpoints: []\nunknown: true\n"), FilterSource)
	require.Nil(t, cfg)
	require.ErrorContains(t, err, "field unknown not found")
	var schemaErr *jsonschema.ValidationError
	require.NotErrorAs(t, err, &schemaErr)
}

func TestLoader_ConcurrentLoads(t *testing.T) {
	schema, err := os.ReadFile("../../config.schema.json")
	require.NoError(t, err)
	loader := NewLoader(schema)
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for range cap(errors) {
		wg.Go(func() {
			_, err := loader.Load([]byte("responses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}}]"), PerRouteSource)
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}
