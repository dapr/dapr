// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

import (
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/native"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/components-contrib/exporters/zipkin"
)

// Load message buses
func Load() {
	RegisterExporter("zipkin", func() exporters.Exporter {
		return zipkin.NewZipkinExporter()
	})
	RegisterExporter("string", func() exporters.Exporter {
		return stringexporter.NewStringExporter()
	})
	RegisterExporter("native", func() exporters.Exporter {
		return native.NewNativeExporter()
	})
}
