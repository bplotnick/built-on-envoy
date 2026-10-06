// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// The schema stays at the extension root for tooling; embedding it here supplies
// the configuration loader without duplicating the canonical file.

package impl

import (
	_ "embed"

	"github.com/tetratelabs/built-on-envoy/extensions/composer/dynamic-fault-injection/internal/config"
)

//go:embed config.schema.json
var configSchemaJSON []byte

var filterConfigLoader = config.NewLoader(configSchemaJSON)
